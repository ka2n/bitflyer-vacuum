package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/cheggaaa/pb"
	"golang.org/x/time/rate"
)

const (
	Count      = 500
	DebugLimit = 0
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
		product     string
		startID     int64
		bar         *pb.ProgressBar
		proxyAPIKey string
		reqPerM     int
	)
	flag.StringVar(&proxyAPIKey, "proxy-key", "", "Proxy API Key")
	flag.StringVar(&product, "product", "BTC_JPY", "Product")
	flag.Int64Var(&startID, "start", 636150891, "StartID")
	flag.IntVar(&reqPerM, "reqpm", 500, "Request per minute")
	flag.Parse()

	os.MkdirAll(fmt.Sprintf("results/%s", product), 0777)

	ctx := context.Background()

	// プロキシ一覧を取得
	balancer, err := getNewProxyBalancer(proxyAPIKey)
	if err != nil {
		panic(err)
	}
	if balancer.Len() == 0 {
		panic("No proxy available")
	}
	fmt.Printf("%d proxies available\n", balancer.Len())

	// HTTP Clientを取得
	clients := make([]*http.Client, balancer.Len())
	for i := range clients {
		purl := balancer.Next()
		clients[i] = newProxiedHTTPClient(purl)
	}
	// クライアントのリソースプール
	clientpool := make(chan Client, len(clients))
	for _, c := range clients {
		limit := rate.NewLimiter(rate.Every(time.Minute/time.Duration(reqPerM)), 5)
		clientpool <- Client{HTTP: c, Limiter: limit}
	}

	// queue -> ダウンロード -> dlPipe
	queue := getQueue(int(startID), product)
	dlPipe := make(chan Queue)
	go func() {
		defer close(dlPipe)
		var wg sync.WaitGroup
		for q := range queue {
			// あるファイルは飛ばす
			if isExist(q.URL, q.ID, product) {
				dlPipe <- q
				continue
			}

			// 未取得が現れた時点でプログレスバー出現
			if bar == nil {
				// バーを初期化
				bar = pb.StartNew(int(q.ID) / Count)
			}

			// 開始
			q := q
			client := <-clientpool
			wg.Add(1)
			go func() {
				defer func() { clientpool <- client }()
				defer wg.Done()
				ctx, cancel := context.WithTimeout(ctx, time.Second*30)
				defer func() { dlPipe <- q }()
				defer cancel()

				if err := client.Limiter.Wait(ctx); err != nil {
					q.Error = err
					return
				}
				q.Body, q.Error = download(client.HTTP, q.URL)
			}()
		}
		wg.Wait()
	}()

	// dlQueue -> ファイルを保存 -> result
	results := make(chan Queue)
	go func() {
		defer close(results)
		var wg sync.WaitGroup
		saveSem := make(chan struct{}, 100)

		for q := range dlPipe {
			if q.Error != nil {
				results <- q
				continue
			}

			q := q
			saveSem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-saveSem }()
				defer wg.Done()
				defer func() { results <- q }()

				if q.Body != nil {
					defer q.Body.Close()
				}
				q.SaveError = saveFile(product, q.ID, q.Body)
			}()
		}
		wg.Wait()
	}()

	// 結果を保存
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
	if bar != nil {
		bar.FinishPrint("Done!")
	}
}

func getNewProxyBalancer(apiKey string) (Balancer, error) {
	b := NewBalancer()
	resp, err := http.Get("https://proxy6.net/api/" + apiKey + "/getproxy")
	if err != nil {
		return b, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return b, errors.New("invalid status: " + resp.Status)
	}

	var p ProxyResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&p); err != nil {
		return b, err
	}

	urls := make([]*url.URL, 0, len(p.List))
	for _, pp := range p.List {
		if pp.Active != "1" {
			continue
		}
		u, err := url.Parse(fmt.Sprintf("http://%s:%s@%s:%s", pp.User, pp.Pass, pp.Host, pp.Port))
		if err != nil {
			continue
		}
		urls = append(urls, u)
		if DebugLimit > 0 && len(urls) >= DebugLimit {
			break
		}
	}

	b.SetURLs(urls)

	return b, nil
}

type ProxyResponse struct {
	Status    string `json:"status"`
	UserID    string `json:"user_id"`
	Balance   string `json:"balance"`
	Currency  string `json:"currency"`
	ListCount int    `json:"list_count"`
	List      map[string]struct {
		ID          string `json:"id"`
		IP          string `json:"ip"`
		Host        string `json:"host"`
		Port        string `json:"port"`
		User        string `json:"user"`
		Pass        string `json:"pass"`
		Type        string `json:"type"`
		Country     string `json:"country"`
		Date        string `json:"date"`
		DateEnd     string `json:"date_end"`
		Unixtime    int    `json:"unixtime"`
		UnixtimeEnd int    `json:"unixtime_end"`
		Descr       string `json:"descr"`
		Active      string `json:"active"`
	} `json:"list"`
}

type Client struct {
	HTTP    *http.Client
	Limiter *rate.Limiter
}

type Queue struct {
	URL       string
	ID        int
	Body      io.ReadCloser
	Error     error
	SaveError error
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

func download(client *http.Client, u string) (io.ReadCloser, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "curl/7.63.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func saveFile(prefix string, id int, bodyOrEmpty io.Reader) error {
	// ファイルをとりあえず作成
	fpath := fmt.Sprintf("results/%s/%d.json.gz", prefix, id)
	f, err := os.Create(fpath)
	if err != nil {
		return err
	}
	defer f.Close()

	// bodyが空なら空ファイルを作成
	if bodyOrEmpty == nil {
		return nil
	}
	// あればgzip
	return compress(f, bodyOrEmpty)
}

func compress(output io.Writer, input io.Reader) error {
	gz := gzip.NewWriter(output)
	io.Copy(gz, input)
	return gz.Close()
}

func isExist(u string, id int, prefix string) bool {
	fpath := fmt.Sprintf("results/%s/%d.json.gz", prefix, id)
	_, err := os.Stat(fpath)
	if !os.IsNotExist(err) {
		return true
	}
	//	fpath = fmt.Sprintf("results/%s/%d.json", prefix, id)
	//	_, err = os.Stat(fpath)
	//	if !os.IsNotExist(err) {
	//		return true
	//	}
	return false
}
