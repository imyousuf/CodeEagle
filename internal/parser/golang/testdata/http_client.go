package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

func fetchUser(id string) {
	http.Get("/api/users/" + id)
}

func createUser(data string) {
	http.Post("/api/users", "application/json", strings.NewReader(data))
}

func headCheck() {
	http.Head("/health")
}

func postForm() {
	http.PostForm("/api/login", url.Values{"user": {"admin"}})
}

func customRequest(ctx context.Context) {
	req, _ := http.NewRequestWithContext(ctx, "PUT", "/api/users/123", nil)
	client := &http.Client{}
	client.Do(req)
}

func clientGet() {
	c := &http.Client{}
	c.Get("/api/items")
}

func clientPost() {
	c := http.Client{}
	c.Post("/api/items", "application/json", nil)
}
