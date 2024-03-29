// Copyright 2020-2021 The Datafuse Authors.
//
// SPDX-License-Identifier: Apache-2.0.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/google/go-github/v35/github"
	"golang.org/x/oauth2"
)

type GithubClient struct {
	Clt               *github.Client
	Owner             string
	Repo              string
	Pr                int
	Author            string
	LastSHA           string
	CommentBody       string
	AuthorAssociation string
	State             string
	LastTag           string
	Ctx               context.Context
}

func NewGithubClient(ctx context.Context, e *github.IssueCommentEvent, token string) (*GithubClient, error) {
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")

	}
	if token == "" {
		return nil, fmt.Errorf("env var missing")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	return &GithubClient{
		Clt:               github.NewClient(tc),
		Owner:             *e.GetRepo().Owner.Login,
		Repo:              *e.GetRepo().Name,
		Pr:                *e.GetIssue().Number,
		Author:            *e.Sender.Login,
		AuthorAssociation: *e.GetComment().AuthorAssociation,
		CommentBody:       *e.GetComment().Body,
		Ctx:               ctx,
		State:             e.GetIssue().GetState(),
	}, nil
}

func NewGithubClientByPush(ctx context.Context, e *github.PushEvent, token string) (*GithubClient, error) {
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")

	}
	if token == "" {
		return nil, fmt.Errorf("env var missing")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	return &GithubClient{
		Clt:     github.NewClient(tc),
		Owner:   *e.GetRepo().Owner.Login,
		Repo:    *e.GetRepo().Name,
		Author:  e.GetPusher().GetLogin(),
		LastSHA: e.GetHeadCommit().GetID(),
		Ctx:     ctx,
	}, nil
}

func (c GithubClient) PostComment(commentBody string) error {
	issueComment := &github.IssueComment{Body: github.String(commentBody)}
	_, _, err := c.Clt.Issues.CreateComment(c.Ctx, c.Owner, c.Repo, c.Pr, issueComment)
	return err
}

func (c GithubClient) GetIssueState() string {
	return c.State
}

func (c GithubClient) CreateLabel(labelName string) error {
	benchmarkLabel := []string{labelName}
	_, _, err := c.Clt.Issues.AddLabelsToIssue(c.Ctx, c.Owner, c.Repo, c.Pr, benchmarkLabel)
	return err
}

func (c *GithubClient) GetLastCommitSHA() string {
	// https://developer.github.com/v3/pulls/#list-commits-on-a-pull-request
	listops := &github.ListOptions{Page: 1, PerPage: 250}
	l, _, _ := c.Clt.PullRequests.ListCommits(c.Ctx, c.Owner, c.Repo, c.Pr, listops)
	if len(l) == 0 {
		return ""
	}
	c.LastSHA = l[len(l)-1].GetSHA()
	return l[len(l)-1].GetSHA()
}

// return true if some workflow run in the latest commit are in progress or failure
func (c *GithubClient) HasUnfinishedWorkflow() (bool, string) {
	Options := &github.ListWorkflowRunsOptions{Actor: c.Author, Event: "pull_request", Status: "queued", ListOptions: github.ListOptions{Page: 1, PerPage: 250}}
	l, _, _ := c.Clt.Actions.ListRepositoryWorkflowRuns(c.Ctx, c.Owner, c.Repo, Options)
	log.Printf("current queued workflow number %d", len(l.WorkflowRuns))
	for _, workflow := range l.WorkflowRuns {
		if workflow.GetHeadSHA() == c.GetLastCommitSHA() {
			return true, ""
		}
	}

	Options = &github.ListWorkflowRunsOptions{Actor: c.Author, Event: "pull_request", Status: "in_progress", ListOptions: github.ListOptions{Page: 1, PerPage: 250}}
	l, _, _ = c.Clt.Actions.ListRepositoryWorkflowRuns(c.Ctx, c.Owner, c.Repo, Options)
	log.Printf("current in-progress workflow number %d", len(l.WorkflowRuns))
	for _, workflow := range l.WorkflowRuns {
		if workflow.GetHeadSHA() == c.GetLastCommitSHA() {
			return true, ""
		}
	}
	Options = &github.ListWorkflowRunsOptions{Actor: c.Author, Event: "pull_request", Status: "failure", ListOptions: github.ListOptions{Page: 1, PerPage: 250}}
	l, _, _ = c.Clt.Actions.ListRepositoryWorkflowRuns(c.Ctx, c.Owner, c.Repo, Options)
	log.Printf("current failed workflow number %d", len(l.WorkflowRuns))
	for _, workflow := range l.WorkflowRuns {
		if workflow.GetHeadSHA() == c.GetLastCommitSHA() {
			return true, *workflow.HTMLURL
		}
	}
	return false, ""
}

func (c *GithubClient) ListAssociatedPR(sha string) []*github.PullRequest {
	Options := &github.PullRequestListOptions{ListOptions: github.ListOptions{Page: 1, PerPage: 250}}
	l, _, _ := c.Clt.PullRequests.ListPullRequestsWithCommit(c.Ctx, c.Owner, c.Repo, sha, Options)
	return l
}

func (c GithubClient) CreateRepositoryDispatch(eventType string, clientPayload map[string]string) error {
	allArgs, err := json.Marshal(clientPayload)
	if err != nil {
		return fmt.Errorf("%v: could not encode client payload", err)
	}
	cp := json.RawMessage(string(allArgs))

	rd := github.DispatchRequestOptions{
		EventType:     eventType,
		ClientPayload: &cp,
	}

	log.Printf("creating repository_dispatch with payload: %v", string(allArgs))
	_, _, err = c.Clt.Repositories.Dispatch(c.Ctx, c.Owner, c.Repo, rd)
	return err
}

func (c GithubClient) UpdateStatus(statusName, state, targetUrl string) error {
	status := github.RepoStatus{State: &state, Context: &statusName, TargetURL: &targetUrl}
	_, _, err := c.Clt.Repositories.CreateStatus(c.Ctx, c.Owner, c.Repo, c.LastSHA, &status)
	return err
}

func (c GithubClient) GetLatestTag() (string, error) {
	listops := &github.ListOptions{Page: 1, PerPage: 250}
	tags, _, err := c.Clt.Repositories.ListTags(context.Background(), c.Owner, c.Repo, listops)
	if err != nil {
		return "", err
	}
	if len(tags) > 0 {
		c.LastTag = tags[0].GetName()
	} else {
		return "", fmt.Errorf("%s owned by %s has no tags", c.Repo, c.Owner)
	}
	return c.LastTag, nil
}
