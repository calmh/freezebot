package main

import (
	"_go/src/io/ioutil"
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

const retries = 5

type configEntry struct {
	Owner      string
	Repos      []string
	Directives []configDirective
}

type configDirective struct {
	State          string
	DaysClosed     int
	DaysNotUpdated int
	Label          string
	Lock           bool
}

func main() {
	token := flag.String("token", os.Getenv("GITHUB_TOKEN"), "GitHub token")
	cfgFile := flag.String("config", "config.json", "Configuration file")
	flag.Parse()

	log.SetOutput(os.Stdout)

	bs, err := ioutil.ReadFile(*cfgFile)
	if err != nil {
		log.Println("Reading config:", err)
		os.Exit(1)
	}

	var cfgs []configEntry
	if err := json.Unmarshal(bs, &cfgs); err != nil {
		log.Println("Reading config:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	for _, cfg := range cfgs {
		if cfg.Owner == "" {
			log.Println("Every config entry must set `owner`")
			os.Exit(2)
		}

		for _, repo := range cfg.Repos {
			log.Printf("Processing %s/%s", cfg.Owner, repo)
			handleRepo(ctx, client, cfg.Owner, repo, cfg.Directives)
		}
		if len(cfg.Repos) > 0 {
			// We're done
			continue
		}

		listOpts := &github.RepositoryListOptions{
			ListOptions: github.ListOptions{
				PerPage: 100,
			},
		}

		for {
			rs, resp, err := client.Repositories.List(ctx, cfg.Owner, listOpts)
			if err != nil {
				log.Println(err)
				os.Exit(1)
			}

			for _, repo := range rs {
				log.Println("Processing", repo.GetFullName())
				handleRepo(ctx, client, cfg.Owner, repo.GetName(), cfg.Directives)
			}

			if resp.NextPage == 0 {
				break
			}
			listOpts.Page = resp.NextPage
		}
	}
}

func handleRepo(ctx context.Context, client *github.Client, owner, repo string, directives []configDirective) {
	for _, directive := range directives {
		opts := &github.IssueListByRepoOptions{
			ListOptions: github.ListOptions{
				PerPage: 100,
			},
		}

		if directive.State != "" {
			opts.State = directive.State
		}

		for {
			is, resp, err := client.Issues.ListByRepo(ctx, owner, repo, opts)
			if err != nil {
				log.Println(err)
				os.Exit(1)
			}

			for _, i := range is {
				if i.GetLocked() {
					// Never touch locked issues
					continue
				}
				if directive.DaysClosed > 0 && daysSince(i.GetClosedAt()) < directive.DaysClosed {
					// Check days closed if set
					continue
				}
				if directive.DaysNotUpdated > 0 && daysSince(i.GetUpdatedAt()) < directive.DaysNotUpdated {
					// Check days not updated if set
					continue
				}

				if directive.Label != "" && !contains(i.Labels, directive.Label) {
					log.Printf("Labeling issue %d %q", i.GetNumber(), directive.Label)
					labelIssue(ctx, client, owner, repo, i.GetNumber(), directive.Label)
				}

				if directive.Lock {
					log.Printf("Locking issue %d", i.GetNumber())
					lockIssue(ctx, client, owner, repo, i.GetNumber())
				}
			}

			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}
}

func labelIssue(ctx context.Context, client *github.Client, owner, repo string, number int, label string) {
	var err error
	for i := 0; i < retries; i++ {
		_, _, err = client.Issues.AddLabelsToIssue(ctx, owner, repo, number, []string{label})
		if err == nil {
			return
		}
		log.Printf("Adding label to issue %d: %v (retrying)\n", number, err)
		time.Sleep(time.Duration(i) * time.Second)
	}
	if err != nil {
		log.Printf("Adding label to issue %d: %v\n", number, err)
		os.Exit(1)
	}
}

func lockIssue(ctx context.Context, client *github.Client, owner, repo string, number int) {
	var err error
	for i := 0; i < retries; i++ {
		_, err := client.Issues.Lock(ctx, owner, repo, number)
		if err == nil {
			return
		}
		log.Printf("Locking issue %d: %v (retrying)\n", number, err)
		time.Sleep(time.Duration(i) * time.Second)
	}
	if err != nil {
		log.Printf("Locking issue %d: %v\n", number, err)
		os.Exit(1)
	}
}

func daysSince(t time.Time) int {
	return int(time.Since(t) / 24 / time.Hour)
}

func contains(l []github.Label, t string) bool {
	for _, s := range l {
		if s.GetName() == t {
			return true
		}
	}
	return false
}
