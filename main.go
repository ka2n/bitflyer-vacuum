package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/cheggaaa/pb"
	"golang.org/x/time/rate"
)

const (
	Count = 500
)

func getURL(product string, minRequireID, count int) (string, int) {
	before := minRequireID + 1
	after := minRequireID - count
	if after < 0 {
		after = 1
	}
	return fmt.Sprintf("https://api.bitflyer.com/v1/executions?product_code=%s&count=%d&before=%d&after=%d", product, count, before, after), after
}

func main() {
	var (
		product string
		startID int64
		bar     *pb.ProgressBar
	)
	flag.StringVar(&product, "product", "BTC_JPY", "Product")
	flag.Int64Var(&startID, "start", 636150891, "StartID")
	flag.Parse()

	os.MkdirAll(fmt.Sprintf("results/%s", product), 0777)

	ctx := context.Background()

	results := make(chan Queue)
	go func() {
		defer close(results)
		queue := getQueue(int(startID), product)
		wg := sync.WaitGroup{}
		sem := make(chan struct{}, 5)
		limit := rate.NewLimiter(rate.Every(time.Minute/500), 5)

		for q := range queue {
			if isExist(q.URL, q.ID, product) {
				results <- q
				continue
			} else if bar == nil {
				bar = pb.StartNew(int(q.ID) / Count)
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(q Queue) {
				defer wg.Done()
				defer func() { <-sem }()
				limit.Wait(ctx)
				q.Error = download(q.URL, q.ID, product)
				results <- q
			}(q)
		}
		wg.Wait()
	}()

	errFile, err := os.OpenFile(product+"_result_err.json", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		panic(err)
	}
	defer errFile.Close()

	for r := range results {
		if r.Error != nil {
			fmt.Fprintf(errFile, "%d: %s\n", r.ID, r.Error)
		}
		if bar != nil {
			bar.Increment()
		}
	}
	bar.FinishPrint("Done!")
}

type Queue struct {
	URL   string
	ID    int
	Error error
}

func getQueue(startID int, product string) chan Queue {
	queue := make(chan Queue, 5)
	go func() {
		defer close(queue)
		next := startID

		for {
			u, nid := getURL(product, next, Count)
			queue <- Queue{URL: u, ID: next}
			next = nid

			if nid < 2 {
				break
			}
		}
	}()

	return queue
}

func download(u string, id int, prefix string) error {
	client := http.DefaultClient
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	fpath := fmt.Sprintf("results/%s/%d.json", prefix, id)
	f, err := os.Create(fpath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func isExist(u string, id int, prefix string) bool {
	fpath := fmt.Sprintf("results/%s/%d.json", prefix, id)
	_, err := os.Stat(fpath)
	if !os.IsNotExist(err) {
		return true
	}
	return false
}
