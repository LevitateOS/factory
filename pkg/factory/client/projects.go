// Package client connects Forge to the independently supervised Factory service.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/strictjson"
)

type Client struct{ HTTP *http.Client }

func New(socket string) *Client {
	return &Client{HTTP: &http.Client{
		Timeout: 11 * time.Minute,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *Client) call(ctx context.Context, in factory.ProjectServiceRequest) (factory.ProjectServiceResult, error) {
	var out factory.ProjectServiceResult
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(in); err != nil {
		return out, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://factory/projects", &body)
	if err != nil {
		return out, err
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(r)
	if err != nil {
		return out, errors.New("factory service unavailable; operation was not retried")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return out, factory.ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return out, errors.New("factory project operation unconfirmed")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (512<<10)+1))
	if err != nil || len(data) > 512<<10 || strictjson.Decode(bytes.NewReader(data), &out) != nil {
		return out, errors.New("invalid factory project response")
	}
	return out, nil
}

func (c *Client) StopProject(ctx context.Context, principal, project string) (factory.Withdrawal, []factory.RunStopOutcome, error) {
	out, err := c.call(ctx, factory.ProjectServiceRequest{Action: "stop", Project: project, Principal: principal})
	return out.Withdrawal, out.Runs, err
}

func (c *Client) VerifyProjectStart(ctx context.Context, project string) (factory.StartVerification, error) {
	out, err := c.call(ctx, factory.ProjectServiceRequest{Action: "verify-start", Project: project})
	return out.Verification, err
}

func (c *Client) ClearProjectStopAfterStart(ctx context.Context, project string, verification factory.StartVerification) (bool, error) {
	out, err := c.call(ctx, factory.ProjectServiceRequest{Action: "complete-start", Project: project, Verification: verification})
	return out.DispatchOpen, err
}

func (c *Client) EffectiveAuthority(ctx context.Context, repository int64) (factory.EffectiveAuthority, error) {
	out, err := c.call(ctx, factory.ProjectServiceRequest{Action: "authority", Repository: repository})
	return out.Authority, err
}
