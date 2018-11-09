package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
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
	Query          string
	State          string
	DaysClosed     int
	DaysNotUpdated int
	Label          string
	Lock           bool
	Close          bool
	CloseComment   string
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
			handleRepoIssues(ctx, client, cfg.Owner, repo, cfg.Directives)
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
				handleRepoIssues(ctx, client, cfg.Owner, repo.GetName(), cfg.Directives)
			}

			if resp.NextPage == 0 {
				break
			}
			listOpts.Page = resp.NextPage
		}
	}
}

func handleRepoIssues(ctx context.Context, client *github.Client, owner, repo string, directives []configDirective) {
	for _, directive := range directives {
		issues, err := findIssues(ctx, client, owner, repo, directive)
		if err != nil {
			log.Println("Finding issues:", err)
			os.Exit(1)
		}

		for _, i := range issues {
			handleIssue(ctx, client, owner, repo, i, directive)
		}
	}
}

func findIssues(ctx context.Context, client *github.Client, owner, repo string, directive configDirective) ([]github.Issue, error) {
	if directive.Query != "" {
		return findIssuesByQuery(ctx, client, owner, repo, directive)
	}
	return findIssuesByList(ctx, client, owner, repo, directive)
}

func findIssuesByList(ctx context.Context, client *github.Client, owner, repo string, directive configDirective) ([]github.Issue, error) {
	opts := &github.IssueListByRepoOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	if directive.State != "" {
		opts.State = directive.State
	}

	var res []github.Issue

	for {
		is, resp, err := client.Issues.ListByRepo(ctx, owner, repo, opts)
		if err != nil {
			return nil, err
		}

		for _, i := range is {
			res = append(res, *i)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return res, nil
}

func findIssuesByQuery(ctx context.Context, client *github.Client, owner, repo string, directive configDirective) ([]github.Issue, error) {
	opts := &github.SearchOptions{
		Sort:  "created",
		Order: "asc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	query := fmt.Sprintf("%s repo:%s/%s", directive.Query, owner, repo)
	var res []github.Issue

	for {
		is, resp, err := client.Search.Issues(ctx, query, opts)
		if err != nil {
			return nil, err
		}

		res = append(res, is.Issues...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return res, nil
}

func handleIssue(ctx context.Context, client *github.Client, owner, repo string, i github.Issue, directive configDirective) {
	if i.GetLocked() {
		// Never touch locked issues
		return
	}
	if directive.DaysClosed > 0 && daysSince(i.GetClosedAt()) < directive.DaysClosed {
		// Check days closed if set
		return
	}
	if directive.DaysNotUpdated > 0 && daysSince(i.GetUpdatedAt()) < directive.DaysNotUpdated {
		// Check days not updated if set
		return
	}

	if directive.Label != "" && !contains(i.Labels, directive.Label) {
		log.Printf("Labeling issue %d %q", i.GetNumber(), directive.Label)
		labelIssue(ctx, client, owner, repo, i.GetNumber(), directive.Label)
	}

	if directive.Close && i.GetState() != "closed" {
		if directive.CloseComment != "" {
			log.Printf("Commenting on issue %d", i.GetNumber())
			commentIssue(ctx, client, owner, repo, i.GetNumber(), directive.CloseComment)
		}
		log.Printf("Closing issue %d", i.GetNumber())
		closeIssue(ctx, client, owner, repo, i.GetNumber())
	}

	if directive.Lock {
		log.Printf("Locking issue %d", i.GetNumber())
		lockIssue(ctx, client, owner, repo, i.GetNumber())
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
		_, err := client.Issues.Lock(ctx, owner, repo, number, nil)
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

func closeIssue(ctx context.Context, client *github.Client, owner, repo string, number int) {
	var err error
	for i := 0; i < retries; i++ {
		_, _, err := client.Issues.Edit(ctx, owner, repo, number, &github.IssueRequest{State: github.String("closed")})
		if err == nil {
			return
		}
		log.Printf("Closing issue %d: %v (retrying)\n", number, err)
		time.Sleep(time.Duration(i) * time.Second)
	}
	if err != nil {
		log.Printf("Closing issue %d: %v\n", number, err)
		os.Exit(1)
	}
}

func commentIssue(ctx context.Context, client *github.Client, owner, repo string, number int, comment string) {
	var err error
	for i := 0; i < retries; i++ {
		_, _, err := client.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: github.String(comment)})
		if err == nil {
			return
		}
		log.Printf("Commenting on issue %d: %v (retrying)\n", number, err)
		time.Sleep(time.Duration(i) * time.Second)
	}
	if err != nil {
		log.Printf("Commenting on issue %d: %v\n", number, err)
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
