// Copyright 2024 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/urfave/cli/v3"
)

const (
	cloudAPIServerURL = "http://cloud-api.livekit.run"
	cloudDashboardURL = "https://cloud.livekit.run"
	// cloudAPIServerURL    = "https://cloud-api.livekit.io"
	// cloudDashboardURL    = "https://cloud.livekit.io"
	createTokenEndpoint  = "/cli/auth"
	confirmAuthEndpoint  = "/cli/confirm-auth"
	claimSessionEndpoint = "/cli/claim"
)

type CreateTokenResponse struct {
	Identifier string
	Token      string
	Expires    int64
	DeviceName string
}

type AuthClient struct {
	client            *http.Client
	baseURL           string
	verificationToken CreateTokenResponse
}

func (a *AuthClient) GetVerificationToken(subdomain string) (*CreateTokenResponse, error) {
	reqURL, err := url.Parse(a.baseURL + createTokenEndpoint)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Add("device_name", "CLI")
	params.Add("subdomain", subdomain)
	reqURL.RawQuery = params.Encode()

	resp, err := a.client.Post(reqURL.String(), "application/json", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&a.verificationToken)
	if err != nil {
		return nil, err
	}

	return &a.verificationToken, nil
}

func (a *AuthClient) ClaimSession() (*CreateTokenResponse, error) {
	if a.verificationToken.Token == "" || time.Now().Unix() > a.verificationToken.Expires {
		return nil, errors.New("session expired")
	}

	reqURL, err := url.Parse(a.baseURL + claimSessionEndpoint)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Add("t", a.verificationToken.Token)
	reqURL.RawQuery = params.Encode()

	resp, err := a.client.Get(reqURL.String())
	if err != nil {
		return nil, err
	}

	sessionToken := &CreateTokenResponse{}
	err = json.NewDecoder(resp.Body).Decode(&sessionToken)
	if err != nil {
		return nil, err
	}

	fmt.Println("SESSIONTOKEN: ", sessionToken.Token, sessionToken.DeviceName)

	return sessionToken, nil
}

func (a *AuthClient) Deauthenticate() error {
	// TODO: revoke any session token
	return nil
}

func NewAuthClient(client *http.Client, baseURL string) *AuthClient {
	a := &AuthClient{
		client:  client,
		baseURL: baseURL,
	}
	return a
}

var (
	disconnect   bool
	authClient   AuthClient
	AuthCommands = []*cli.Command{
		{
			Name:     "auth",
			Usage:    "Add or remove projects and view existing project properties",
			Category: "Core",
			Before:   createAuthClient,
			Action:   handleAuth,
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:        "d",
					Aliases:     []string{"disconnect"},
					Destination: &disconnect,
				},
			},
		},
	}
)

func createAuthClient(ctx context.Context, cmd *cli.Command) error {
	if err := loadProjectConfig(ctx, cmd); err != nil {
		return err
	}
	authClient = *NewAuthClient(&http.Client{}, cloudAPIServerURL)
	return nil
}

func handleAuth(ctx context.Context, cmd *cli.Command) error {
	if disconnect {
		return authClient.Deauthenticate()
	}
	return tryAuthIfNeeded(ctx, cmd)
}

func tryAuthIfNeeded(ctx context.Context, cmd *cli.Command) error {
	_, err := loadProjectDetails(cmd)
	if err != nil {
		return err
	}

	// TODO: if we already have a valid session token, return early

	fmt.Println("Requesting verification token...")
	token, err := authClient.GetVerificationToken("bar.foo") // FIXME: subdomain?
	if err != nil {
		return err
	}

	authURL, err := url.Parse(cloudDashboardURL + confirmAuthEndpoint)
	if err != nil {
		return err
	}
	params := url.Values{}
	params.Add("t", token.Token)
	authURL.RawQuery = params.Encode()

	fmt.Println(authURL)

	return pollClaim(ctx, cmd)
}

func pollClaim(context.Context, *cli.Command) error {
	claim := make(chan *CreateTokenResponse)
	cancel := make(chan error)
	go func() {
		for {
			fmt.Println("Polling...")
			time.Sleep(10 * time.Second)
			session, err := authClient.ClaimSession()
			if err != nil {
				cancel <- err
				return
			}
			fmt.Println(session)
			claim <- session
		}
	}()

	select {
	case <-time.After(1 * time.Minute):
		return errors.New("session claim timed out")
	case err := <-cancel:
		return err
	case sessionToken := <-claim:
		// TODO: write to config file
		fmt.Println(sessionToken)
		return nil
	}
}