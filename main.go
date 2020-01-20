package main

import (
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gophercises/quiet_hn/hn"
)

func main() {
	// parse flags
	var port, numStories int
	flag.IntVar(&port, "port", 3000, "the port to start the web server on")
	flag.IntVar(&numStories, "num_stories", 30, "the number of top stories to display")
	flag.Parse()

	tpl := template.Must(template.ParseFiles("./index.gohtml"))

	http.HandleFunc("/", handler(numStories, tpl))

	// Start the server
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func handler(numStories int, tpl *template.Template) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		stories, err := getStories()
		if err != nil {
			http.Error(w, "Failed to load top stories", http.StatusInternalServerError)
		}

		fmt.Println("creating template")
		data := templateData{
			Stories: stories,
			Time:    time.Now().Sub(start),
		}
		err = tpl.Execute(w, data)
		if err != nil {
			http.Error(w, "Failed to process the template", http.StatusInternalServerError)
			return
		}
	})
}

type storyCache struct {
	Items      []item
	Expiration time.Time
	sync.Mutex
}

func (c *storyCache) Empty() bool {
	return len(c.Items) == 0
}

func (c *storyCache) Clear() {
	c.Items = []item{}
}

// Global
var cache storyCache

func getStories() ([]item, error) {
	cache.Lock()
	defer cache.Unlock()

	var client hn.Client

	ids, err := client.TopItems()
	if err != nil {
		return nil, errors.New("something went wrong")
	}

	done := make(chan interface{})
	defer close(done)

	if !cache.Empty() && time.Now().Sub(cache.Expiration) < 0 {
		fmt.Println("returning cached stories")
		return cache.Items, nil
	}

	cache.Clear()
	for result := range take(done, getItems(done, ids, client), 30) {
		cache.Items = append(cache.Items, result.item)
	}
	cache.Expiration = time.Now().Add(time.Second * 3)

	// Currently not really working?
	fmt.Println("sorting stories")
	sort.Slice(cache.Items, func(i, j int) bool {
		return cache.Items[i].ID > cache.Items[j].ID
	})

	return cache.Items, nil
}

type result struct {
	index int
	item  item
}

func getItems(done <-chan interface{}, ids []int, client hn.Client) <-chan result {
	var wg sync.WaitGroup
	iwStream := make(chan result)

	for _, id := range ids {
		wg.Add(1)
		go func(i int) {
			select {
			case <-done:
				return
			default:
			}

			hnItem, err := client.GetItem(i)
			if err != nil {
				return
			}
			item := parseHNItem(hnItem)
			if isStoryLink(item) {
				iwStream <- result{index: i, item: item}
			}
			wg.Done()
		}(id)
	}

	go func() {
		wg.Wait()
		close(iwStream)
	}()

	fmt.Println("closing from getItems")
	return iwStream
}

func take(done <-chan interface{}, valueStream <-chan result, count int) <-chan result {
	takeStream := make(chan result)
	go func() {
		defer close(takeStream)
		for i := count; i > 0 || i == -1; {
			if i != -1 {
				i--
			}
			select {
			case <-done:
				return
			case takeStream <- <-valueStream:
			}
		}
	}()

	fmt.Println("closing from take")
	return takeStream
}

func isStoryLink(item item) bool {
	return item.Type == "story" && item.URL != ""
}

func parseHNItem(hnItem hn.Item) item {
	ret := item{Item: hnItem}
	url, err := url.Parse(ret.URL)
	if err == nil {
		ret.Host = strings.TrimPrefix(url.Hostname(), "www.")
	}
	return ret
}

// item is the same as the hn.Item, but adds the Host field
type item struct {
	hn.Item
	Host string
}

type templateData struct {
	Stories []item
	Time    time.Duration
}
