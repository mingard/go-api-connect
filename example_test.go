// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	apiconnect "github.com/mingard/go-api-connect"
)

// Book is a document in the books collection.
type Book struct {
	ID    string `json:"_id,omitempty"`
	Title string `json:"title"`
	Year  int    `json:"year"`
}

// Reads a page of a collection, then adds, updates and deletes a document.
// The README's Usage section shows the same code.
func Example() {
	// The property is the API version and the database: /1.0/library/books.
	api := apiconnect.New("https", "api.example.com", "1.0/library",
		os.Getenv("DADI_CLIENT_ID"), os.Getenv("DADI_SECRET"), 443)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Books from 1965 on whose title starts with "Dune", newest first.
	get := &apiconnect.Get{
		Collection: "books",
		Limit:      20,
		Sort:       apiconnect.NewSort().Desc("year").Asc("title"),
		Query: apiconnect.NewFilters(
			apiconnect.StrFilter("title", apiconnect.Regex, "^Dune"),
			apiconnect.NumFilter("year", apiconnect.GreaterThanOrEqualTo, 1965),
		),
	}
	var page struct {
		Results []Book `json:"results"`
	}
	if _, _, err := api.Do(ctx, get, &page); err != nil {
		if errors.Is(err, apiconnect.ErrToken) {
			fmt.Println("no token; check the client id and secret:", err)
			return
		}
		fmt.Println(err)
		return
	}
	for _, b := range page.Results {
		fmt.Println(b.Title, b.Year)
	}

	body, err := json.Marshal(Book{Title: "Children of Dune", Year: 1976})
	if err != nil {
		fmt.Println(err)
		return
	}
	var created struct {
		Results []Book `json:"results"`
	}
	if _, _, err := api.Do(ctx, &apiconnect.Post{Collection: "books", Body: body}, &created); err != nil {
		fmt.Println(err)
		return
	}
	if len(created.Results) == 0 {
		return
	}
	id := created.Results[0].ID

	update := []byte(`{"title":"Children of Dune (first edition)"}`)
	if _, _, err := api.Do(ctx, &apiconnect.Put{Collection: "books", ID: id, Body: update}, nil); err != nil {
		fmt.Println(err)
		return
	}
	if _, _, err := api.Do(ctx, &apiconnect.Delete{Collection: "books", ID: id}, nil); err != nil {
		fmt.Println(err)
	}
}

// Sends every request, the token request included, through a client of
// your own, here one with a proxy: set Instance.HTTPClient.
func ExampleInstance_customClient() {
	api := apiconnect.New("https", "api.example.com", "1.0/library",
		os.Getenv("DADI_CLIENT_ID"), os.Getenv("DADI_SECRET"), 443)
	api.HTTPClient = &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
	}
	tok, err := api.Token(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = tok
}

func ExampleNewFilters() {
	f := apiconnect.NewFilters(
		apiconnect.StrFilter("title", apiconnect.Regex, `^Dune\b`),
		apiconnect.NumFilter("year", apiconnect.GreaterThanOrEqualTo, 1965),
		apiconnect.IsOneOf("format", []string{"hardback", "paperback"}),
	)
	fmt.Println(f.ToQueryString())
	// Output: {"title": {"$regex": "^Dune\\b"},"year": {"$gte": 1965},"format": {"$in": ["hardback","paperback"]}}
}

func ExampleSort() {
	fmt.Println(apiconnect.NewSort().Desc("year").Asc("title"))
	// Output: {"year": -1,"title": 1}
}
