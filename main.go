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

var cache storyCache

func getStories() ([]item, error) {
	cache.Lock()
	defer cache.Unlock()

	var out []item
	var client hn.Client

	ids, err := client.TopItems()
	if err != nil {
		return out, errors.New("something went wrong")
	}
	done := make(chan interface{})
	defer close(done)

	if !cache.Empty() && time.Now().Sub(cache.Expiration) > 0 {
		return cache.Items, nil
	}

	for item := range take(done, getItems(done, ids, client), 30) {
		out = append(out, item)
	}
	cache.Items = out
	cache.Expiration = time.Now().Add(time.Second * 30)

	fmt.Println("sorting stories")
	sort.Slice(cache.Items, func(i, j int) bool {
		return cache.Items[i].ID > cache.Items[j].ID
	})

	return cache.Items, nil
}

func getItems(done <-chan interface{}, ids []int, client hn.Client) <-chan item {
	var wg sync.WaitGroup
	iwStream := make(chan item)

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
				// fmt.Printf("received story %v", item)
				iwStream <- item
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

func take(done <-chan interface{}, valueStream <-chan item, count int) <-chan item {
	takeStream := make(chan item, count)
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

type itemFilter func(item item) bool

func isStoryLinkFunc() itemFilter {
	return func(it item) bool {
		return it.Type == "story" && it.URL != ""
	}
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

type storyCache struct {
	Items      []item
	Expiration time.Time
	sync.Mutex
}

func (c storyCache) Empty() bool {
	return len(c.Items) == 0
}

// var stories []item
// for _, id := range ids {
// 	hnItem, err := client.GetItem(id)
// 	if err != nil {
// 		continue
// 	}
// 	item := parseHNItem(hnItem)
// 	if isStoryLink(item) {
// 		stories = append(stories, item)
// 		if len(stories) >= numStories {
// 			break
// 		}
// 	}
// }
