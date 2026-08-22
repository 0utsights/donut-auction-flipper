package donutapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"donut-network/internal/market"
)

type Config struct {
	BaseURL, APIKey   string
	RequestsPerMinute int
	MaxRetries        int
	Timeout           time.Duration
}
type Client struct {
	cfg         Config
	http        *http.Client
	mu          sync.Mutex
	nextRequest time.Time
	statsMu     sync.RWMutex
	stats       Stats
}

type Stats struct {
	Requests           uint64    `json:"requests"`
	Errors             uint64    `json:"errors"`
	Retries            uint64    `json:"retries"`
	RateLimitResponses uint64    `json:"rate_limit_responses"`
	LastLatencyMS      float64   `json:"last_latency_ms"`
	LastSuccessAt      time.Time `json:"last_success_at,omitempty"`
	LastErrorAt        time.Time `json:"last_error_at,omitempty"`
}

func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.donutsmp.net"
	}
	if cfg.RequestsPerMinute <= 0 || cfg.RequestsPerMinute > 250 {
		cfg.RequestsPerMinute = 240
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}
func NewWithHTTP(cfg Config, h *http.Client) *Client { c := New(cfg); c.http = h; return c }

type itemData struct {
	Enchantments struct {
		Levels map[string]int `json:"levels"`
	} `json:"enchantments"`
	Trim struct {
		Material string `json:"material"`
		Pattern  string `json:"pattern"`
	} `json:"trim"`
}
type item struct {
	ID          string   `json:"id"`
	Count       int      `json:"count"`
	DisplayName string   `json:"display_name"`
	Lore        []string `json:"lore"`
	Enchants    itemData `json:"enchants"`
	Contents    []item   `json:"contents"`
}
type seller struct{ Name, UUID string }

func (s *seller) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name string `json:"name"`
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Name = raw.Name
	s.UUID = raw.UUID
	return nil
}

type auction struct {
	Item     item        `json:"item"`
	Price    json.Number `json:"price"`
	Seller   seller      `json:"seller"`
	TimeLeft int64       `json:"time_left"`
}
type purchase struct {
	Item   item        `json:"item"`
	Price  json.Number `json:"price"`
	Seller seller      `json:"seller"`
	SoldAt int64       `json:"unixMillisDateSold"`
}

func (c *Client) AuctionPage(ctx context.Context, page int, search, sortOrder string) ([]market.Listing, error) {
	if page < 1 {
		return nil, errors.New("page must be >= 1")
	}
	body := map[string]string{}
	if search != "" {
		body["search"] = search
	}
	if sortOrder != "" {
		body["sort"] = sortOrder
	}
	var response struct {
		Result []auction `json:"result"`
		Status int       `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/auction/list/%d", page), body, &response); err != nil {
		return nil, err
	}
	if response.Status != http.StatusOK {
		c.recordDecodeError()
		return nil, fmt.Errorf("donut API payload status %d", response.Status)
	}
	now := time.Now().UTC()
	out := make([]market.Listing, 0, len(response.Result))
	for _, a := range response.Result {
		price, err := money(a.Price)
		if err != nil || price <= 0 || strings.TrimSpace(a.Item.ID) == "" {
			continue
		}
		l := market.Listing{SellerUUID: a.Seller.UUID, SellerName: a.Seller.Name, Item: convertItem(a.Item), TotalPrice: price, FirstSeen: now, LastSeen: now, Source: market.SourceDonutAPI, SearchContext: search, Page: page}
		// The upstream API reports time_left in milliseconds. Bound it before
		// constructing a time.Duration so malformed values cannot overflow.
		if a.TimeLeft > 0 && a.TimeLeft <= int64((30*24*time.Hour)/time.Millisecond) {
			l.ExpiresAt = now.Add(time.Duration(a.TimeLeft) * time.Millisecond)
		}
		l = market.NormalizeListing(l)
		l.AuthoritativeID = stableListingID(l)
		l.Fingerprint = market.Fingerprint(l.AuthoritativeID, l.SellerUUID+"/"+l.SellerName, l.Signature, l.TotalPrice, l.Item.Quantity)
		out = append(out, l)
	}
	return out, nil
}
func (c *Client) TransactionPage(ctx context.Context, page int) ([]market.Transaction, error) {
	if page < 1 || page > 10 {
		return nil, errors.New("transaction page must be between 1 and 10")
	}
	var response struct {
		Result []purchase `json:"result"`
		Status int        `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/auction/transactions/%d", page), nil, &response); err != nil {
		return nil, err
	}
	if response.Status != http.StatusOK {
		c.recordDecodeError()
		return nil, fmt.Errorf("donut API payload status %d", response.Status)
	}
	out := make([]market.Transaction, 0, len(response.Result))
	now := time.Now().UTC()
	for _, p := range response.Result {
		price, err := money(p.Price)
		soldAt := time.UnixMilli(p.SoldAt).UTC()
		if err != nil || price <= 0 || strings.TrimSpace(p.Item.ID) == "" || p.SoldAt <= 0 || soldAt.After(now.Add(5*time.Minute)) {
			continue
		}
		t := market.Transaction{SellerUUID: p.Seller.UUID, SellerName: p.Seller.Name, Item: convertItem(p.Item), TotalPrice: price, SoldAt: soldAt, Source: market.SourceDonutAPI}
		out = append(out, market.NormalizeTransaction(t))
	}
	return out, nil
}
func (c *Client) AllTransactionPages(ctx context.Context) ([]market.Transaction, error) {
	out := []market.Transaction{}
	for p := 1; p <= 10; p++ {
		ts, err := c.TransactionPage(ctx, p)
		if err != nil {
			return out, fmt.Errorf("page %d: %w", p, err)
		}
		out = append(out, ts...)
		if len(ts) < 100 {
			break
		}
	}
	return out, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var last error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if err := c.rateLimit(ctx); err != nil {
			return err
		}
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseURL, "/")+path, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}
		started := time.Now()
		resp, err := c.http.Do(req)
		latency := time.Since(started)
		c.recordAttempt(latency, err, attempt > 0, resp)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			dec := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
			dec.UseNumber()
			if err := dec.Decode(out); err != nil {
				c.recordDecodeError()
				return fmt.Errorf("decode donut API response: %w", err)
			}
			if err := dec.Decode(&struct{}{}); err != io.EOF {
				c.recordDecodeError()
				return errors.New("decode donut API response: trailing JSON data")
			}
			c.recordSuccess()
			return nil
		}
		if err == nil {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			if resp.StatusCode == http.StatusUnauthorized {
				return fmt.Errorf("donut API authentication failed: %s", snippet)
			}
			last = fmt.Errorf("donut API status %d: %s", resp.StatusCode, snippet)
			if resp.StatusCode != 429 && resp.StatusCode < 500 {
				return last
			}
		} else {
			last = err
		}
		if attempt < c.cfg.MaxRetries {
			delay := time.Duration(1<<attempt) * 100 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return last
}

func (c *Client) Stats() Stats {
	c.statsMu.RLock()
	defer c.statsMu.RUnlock()
	return c.stats
}

func (c *Client) recordAttempt(latency time.Duration, requestErr error, retry bool, response *http.Response) {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	c.stats.Requests++
	c.stats.LastLatencyMS = float64(latency) / float64(time.Millisecond)
	if retry {
		c.stats.Retries++
	}
	if response != nil && response.StatusCode == http.StatusTooManyRequests {
		c.stats.RateLimitResponses++
	}
	if requestErr != nil || (response != nil && (response.StatusCode < 200 || response.StatusCode >= 300)) {
		c.stats.Errors++
		c.stats.LastErrorAt = time.Now().UTC()
	}
}

func (c *Client) recordDecodeError() {
	c.statsMu.Lock()
	c.stats.Errors++
	c.stats.LastErrorAt = time.Now().UTC()
	c.statsMu.Unlock()
}

func (c *Client) recordSuccess() {
	c.statsMu.Lock()
	c.stats.LastSuccessAt = time.Now().UTC()
	c.statsMu.Unlock()
}
func (c *Client) rateLimit(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.nextRequest)
	interval := time.Minute / time.Duration(c.cfg.RequestsPerMinute)
	if wait < 0 {
		wait = 0
	}
	c.nextRequest = time.Now().Add(wait + interval)
	c.mu.Unlock()
	if wait == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}
func convertItem(i item) market.Item {
	return convertItemDepth(i, 0)
}

func convertItemDepth(i item, depth int) market.Item {
	limit := min(len(i.Contents), 128)
	contents := make([]market.Item, 0, limit)
	for _, child := range i.Contents[:limit] {
		if strings.TrimSpace(child.ID) != "" && child.Count > 0 {
			converted := market.Item{ID: child.ID, Quantity: child.Count, DisplayName: child.DisplayName, Lore: child.Lore, Enchantments: child.Enchants.Enchantments.Levels, TrimMaterial: child.Enchants.Trim.Material, TrimPattern: child.Enchants.Trim.Pattern}
			if depth < 3 {
				converted = convertItemDepth(child, depth+1)
			}
			contents = append(contents, converted)
		}
	}
	return market.Item{ID: i.ID, Quantity: i.Count, DisplayName: i.DisplayName, Lore: i.Lore, Enchantments: i.Enchants.Enchantments.Levels, TrimMaterial: i.Enchants.Trim.Material, TrimPattern: i.Enchants.Trim.Pattern, Contents: contents}
}

func stableListingID(listing market.Listing) string {
	// The official API omits a listing ID. Expiry is effectively creation time
	// plus a fixed lifetime, so a five-second bucket remains stable across polls
	// while still distinguishing most legitimate duplicate listings.
	expiryBucket := int64(0)
	if !listing.ExpiresAt.IsZero() {
		expiryBucket = (listing.ExpiresAt.Unix() + 2) / 5
	}
	identity := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d", listing.SellerUUID, listing.SellerName, listing.Signature.Exact, listing.TotalPrice, listing.Item.Quantity, expiryBucket)
	sum := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("donut:%x", sum[:12])
}
func money(n json.Number) (int64, error) {
	if n == "" {
		return 0, errors.New("missing price")
	}
	if i, err := n.Int64(); err == nil {
		return i, nil
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return 0, err
	}
	if f < 0 || f > 9e18 {
		return 0, errors.New("price out of range")
	}
	return int64(f), nil
}
