/*
Copyright 2014 Google & the Go AUTHORS

Go AUTHORS are:
See https://code.google.com/p/go/source/browse/AUTHORS

Licensed under the terms of Go itself:
https://code.google.com/p/go/source/browse/LICENSE
*/

// Package gce provides access to Google Compute Engine (GCE) metadata and
// API service accounts.
package gce

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	projOnce sync.Once
	proj     string
)

// OnGCE reports whether this process is running on Google Compute Engine.
func OnGCE() bool {
	// TODO: maybe something cheaper? this is pretty cheap, though.
	return ProjectID() != ""
}

// ProjectID returns the current instance's project ID string or the empty string
// if not running on GCE.
func ProjectID() string {
	projOnce.Do(setProj)
	return proj
}

func setProj() {
	proj, _ = MetadataValue("project/project-id")
}

// Transport is an HTTP transport that adds authentication headers to
// the request using the default GCE service account and forwards the
// requests to the http package's default transport.
var Transport = NewTransport("default", http.DefaultTransport)

// Client is an http Client that uses the default GCE transport.
var Client = &http.Client{Transport: Transport}

// NewTransport returns a transport that uses the provided GCE
// serviceAccount (optional) to add authentication headers and then
// uses the provided underlying "base" transport.
func NewTransport(serviceAccount string, base http.RoundTripper) http.RoundTripper {
	if serviceAccount == "" {
		serviceAccount = "default"
	}
	return &transport{base: base, acct: serviceAccount}
}

type transport struct {
	base http.RoundTripper
	acct string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// MetadataValue returns a value from the metadata service.
// The suffix is appended to "http://metadata/computeMetadata/v1/".
func MetadataValue(suffix string) (string, error) {
	url := "http://metadata/computeMetadata/v1/" + suffix
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Metadata-Flavor", "Google")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("status code %d trying to fetch %s", res.StatusCode, url)
	}
	all, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(all), nil
}

func (t *transport) getToken() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && t.expires.After(time.Now().Add(2*time.Second)) {
		return t.token, nil
	}
	tokenJSON, err := MetadataValue("instance/service-accounts/" + t.acct + "/token")
	if err != nil {
		return "", err
	}
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(strings.NewReader(tokenJSON)).Decode(&token); err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", errors.New("no access token returned")
	}
	t.token = token.AccessToken
	t.expires = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	return t.token, nil
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.getToken()
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+token)
	return t.base.RoundTrip(req)
}
