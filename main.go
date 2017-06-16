package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

func main() {
	token := flag.String("token", os.Getenv("GITHUB_TOKEN"), "GitHub token")
	owner := flag.String("owner", "syncthing", "Owner")
	repo := flag.String("repo", "syncthing", "Repository")
	closedDays := flag.Int("closed", 365, "Closed cutoff, in days")
	untouchedDays := flag.Int("untouched", 365, "Untouched cutoff, in days")
	label := flag.String("label", "", "Label to add")
	lock := flag.Bool("lock", false, "Lock issues")
	flag.Parse()

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	opts := &github.IssueListByRepoOptions{
		State: "closed",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		is, resp, err := client.Issues.ListByRepo(ctx, *owner, *repo, opts)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		for _, i := range is {
			if i.GetLocked() {
				continue
			}
			if daysSince(i.GetClosedAt()) < *closedDays {
				continue
			}
			if daysSince(i.GetUpdatedAt()) < *untouchedDays {
				continue
			}

			fmt.Printf("Handling issue %d: %s\n", i.GetNumber(), i.GetTitle())

			if *label != "" {
				fmt.Println("Labeling issue", i.GetNumber())
				_, _, err := client.Issues.AddLabelsToIssue(ctx, *owner, *repo, i.GetNumber(), []string{*label})
				if err != nil {
					fmt.Printf("Adding label to issue %d: %v\n", i.GetNumber(), err)
					os.Exit(1)
				}
			}

			if *lock {
				fmt.Println("Locking issue", i.GetNumber())
				_, err := client.Issues.Lock(ctx, *owner, *repo, i.GetNumber())
				if err != nil {
					fmt.Printf("Locking issue %d: %v\n", i.GetNumber(), err)
					os.Exit(1)
				}
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
}

func daysSince(t time.Time) int {
	return int(time.Since(t) / 24 / time.Hour)
}
