package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

const retries = 5

func main() {
	token := flag.String("token", os.Getenv("GITHUB_TOKEN"), "GitHub token")
	owner := flag.String("owner", "", "Owner")
	repo := flag.String("repo", "", "Repository")
	closedDays := flag.Int("closed", 365, "Closed cutoff, in days")
	untouchedDays := flag.Int("untouched", 180, "Untouched cutoff, in days")
	label := flag.String("label", "", "Label to add")
	lock := flag.Bool("lock", false, "Lock issues")
	flag.Parse()

	log.SetOutput(os.Stdout)

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	if *owner == "" {
		log.Println("Need -owner parameter")
		os.Exit(2)
	}

	if *repo != "" {
		handleOldIssues(ctx, client, *owner, *repo, *closedDays, *untouchedDays, *lock, *label)
		return
	}

	opts := &github.RepositoryListOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		rs, resp, err := client.Repositories.List(ctx, *owner, opts)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		for _, repo := range rs {
			log.Println("Processing", repo.GetFullName())
			handleOldIssues(ctx, client, *owner, repo.GetName(), *closedDays, *untouchedDays, *lock, *label)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
}

func handleOldIssues(ctx context.Context, client *github.Client, owner, repo string, closedDays, untouchedDays int, lock bool, label string) {
	opts := &github.IssueListByRepoOptions{
		State: "closed",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		is, resp, err := client.Issues.ListByRepo(ctx, owner, repo, opts)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		for _, i := range is {
			if daysSince(i.GetClosedAt()) < closedDays {
				continue
			}
			if i.GetLocked() {
				continue
			}
			if daysSince(i.GetUpdatedAt()) < untouchedDays && !contains(i.Labels, label) {
				continue
			}

			log.Printf("Handling issue %d: %s\n", i.GetNumber(), i.GetTitle())

			if label != "" && !contains(i.Labels, label) {
				labelIssue(ctx, client, owner, repo, i.GetNumber(), label)
			}

			if lock {
				lockIssue(ctx, client, owner, repo, i.GetNumber())
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
}

func labelIssue(ctx context.Context, client *github.Client, owner, repo string, number int, label string) {
	log.Println("Labeling issue", number)
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
	log.Println("Locking issue", number)
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
