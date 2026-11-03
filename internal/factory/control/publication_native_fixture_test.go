package control_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/levitateos/sodaos/internal/config"
)

type nativeST09Config struct {
	TokenID          int64  `json:"token_id"`
	FountainURL      string `json:"fountain_url"`
	Socket           string `json:"socket"`
	TokenFile        string `json:"token_file"`
	Owner            string `json:"owner"`
	Repo             string `json:"repo"`
	Repository       int64  `json:"repository"`
	ActorID          int64  `json:"actor_id"`
	BaseBranch       string `json:"base_branch"`
	CreateBound      bool   `json:"create_bound"`
	CreatorID        int64  `json:"creator_id"`
	CreatorTokenFile string `json:"creator_token_file"`
}

func nativeAPIContext(parent context.Context, c nativeST09Config, credential, method, path string, body, target any) error {
	var input io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(raw)
	}
	secret, err := config.Secret(credential)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.FountainURL, "/")+path, input)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+secret)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return ctxErr
		}
		return errors.New("native API transport unavailable")
	}
	defer response.Body.Close()
	if ctxErr := nativeContextError(ctx); ctxErr != nil {
		return ctxErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("native fixture API %s %s: status %d", method, path, response.StatusCode)
	}
	if target != nil {
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return ctxErr
		}
		if decodeErr != nil {
			return decodeErr
		}
	}
	if ctxErr := nativeContextError(ctx); ctxErr != nil {
		return ctxErr
	}
	return nil
}
