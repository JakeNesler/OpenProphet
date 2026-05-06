package services

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// NewsItem represents a single news article from the RSS feed
type NewsItem struct {
	Title       string    `xml:"title" json:"title"`
	Link        string    `xml:"link" json:"link"`
	Description string    `xml:"description" json:"description,omitempty"`
	PubDate     string    `xml:"pubDate" json:"pub_date,omitempty"`
	Source      string    `xml:"source" json:"source,omitempty"`
	GUID        string    `xml:"guid" json:"guid,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
}

// NewsItemCompact represents a compact news article with only essential fields
type NewsItemCompact struct {
	Title  string `json:"title"`
	Link   string `json:"link"`
	Source string `json:"source,omitempty"`
}

// ToCompact converts a NewsItem to a compact version
func (n *NewsItem) ToCompact() NewsItemCompact {
	return NewsItemCompact{
		Title:  n.Title,
		Link:   n.Link,
		Source: n.Source,
	}
}

// NewsChannel represents the RSS channel
type NewsChannel struct {
	Title       string      `xml:"title"`
	Link        string      `xml:"link"`
	Description string      `xml:"description"`
	Items       []NewsItem  `xml:"item"`
}

// RSSFeed represents the root RSS structure
type RSSFeed struct {
	XMLName xml.Name    `xml:"rss"`
	Channel NewsChannel `xml:"channel"`
}

// NewsService handles fetching news from various sources
type NewsService struct {
	httpClient *http.Client
}

// NewNewsService creates a new news service
func NewNewsService() *NewsService {
	return &NewsService{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetGoogleNews fetches the latest news from Google News RSS feed
func (ns *NewsService) GetGoogleNews() ([]NewsItem, error) {
	url := "https://news.google.com/rss?hl=en-US&gl=US&ceid=US:en"
	return ns.fetchRSSFeed(url)
}

// GetGoogleNewsByTopic fetches news for a specific topic
// Topics: WORLD, NATION, BUSINESS, TECHNOLOGY, ENTERTAINMENT, SPORTS, SCIENCE, HEALTH
func (ns *NewsService) GetGoogleNewsByTopic(topic string) ([]NewsItem, error) {
	url := fmt.Sprintf("https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRGx6TVdZU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en")

	// Topic-specific URLs
	topicURLs := map[string]string{
		"WORLD":         "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRGx1YlY4U0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"NATION":        "https://news.google.com/rss/topics/CAAqIQgKIhtDQkFTRGdvSUwyMHZNRGxqTjNjd0VnSmxiaWdBUAE?hl=en-US&gl=US&ceid=US:en",
		"BUSINESS":      "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRGx6TVdZU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"TECHNOLOGY":    "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRGRqTVhZU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"ENTERTAINMENT": "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNREpxYW5RU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"SPORTS":        "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRFp1ZEdvU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"SCIENCE":       "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRFp0Y1RjU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"HEALTH":        "https://news.google.com/rss/topics/CAAqIQgKIhtDQkFTRGdvSUwyMHZNR3QwTlRFd0VnSmxiaWdBUAE?hl=en-US&gl=US&ceid=US:en",
	}

	if topicURL, ok := topicURLs[topic]; ok {
		url = topicURL
	}

	return ns.fetchRSSFeed(url)
}

// GetGoogleNewsSearch fetches news for a specific search query
func (ns *NewsService) GetGoogleNewsSearch(query string) ([]NewsItem, error) {
	// Use url.QueryEscape to properly encode the query parameter
	encodedQuery := url.QueryEscape(query)
	urlString := fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=en-US&gl=US&ceid=US:en", encodedQuery)
	return ns.fetchRSSFeed(urlString)
}

// filterStale removes articles with a parsed date older than maxAge.
// Articles with no parsed date (PublishedAt is zero) are kept.
func filterStale(items []NewsItem, maxAge time.Duration) []NewsItem {
	cutoff := time.Now().UTC().Add(-maxAge)
	out := make([]NewsItem, 0, len(items))
	for _, item := range items {
		if item.PublishedAt.IsZero() || item.PublishedAt.After(cutoff) {
			out = append(out, item)
		}
	}
	return out
}

// GetMarketWatchTopStories fetches top stories from MarketWatch
func (ns *NewsService) GetMarketWatchTopStories() ([]NewsItem, error) {
	url := "https://feeds.content.dowjones.io/public/rss/mw_topstories"
	items, err := ns.fetchRSSFeed(url)
	if err != nil {
		return nil, err
	}
	return filterStale(items, 48*time.Hour), nil
}

// GetMarketWatchRealtimeHeadlines fetches real-time headlines from MarketWatch
func (ns *NewsService) GetMarketWatchRealtimeHeadlines() ([]NewsItem, error) {
	url := "https://feeds.content.dowjones.io/public/rss/mw_realtimeheadlines"
	items, err := ns.fetchRSSFeed(url)
	if err != nil {
		return nil, err
	}
	return filterStale(items, 48*time.Hour), nil
}

// GetMarketWatchBulletins fetches breaking news bulletins from MarketWatch
func (ns *NewsService) GetMarketWatchBulletins() ([]NewsItem, error) {
	url := "https://feeds.content.dowjones.io/public/rss/mw_bulletins"
	items, err := ns.fetchRSSFeed(url)
	if err != nil {
		return nil, err
	}
	return filterStale(items, 48*time.Hour), nil
}

// GetMarketWatchMarketPulse fetches market pulse updates from MarketWatch
func (ns *NewsService) GetMarketWatchMarketPulse() ([]NewsItem, error) {
	url := "https://feeds.content.dowjones.io/public/rss/mw_marketpulse"
	items, err := ns.fetchRSSFeed(url)
	if err != nil {
		return nil, err
	}
	return filterStale(items, 48*time.Hour), nil
}

// GetAllMarketWatchNews aggregates all MarketWatch feeds
func (ns *NewsService) GetAllMarketWatchNews() ([]NewsItem, error) {
	allNews := make([]NewsItem, 0)

	feeds := []func() ([]NewsItem, error){
		ns.GetMarketWatchTopStories,
		ns.GetMarketWatchRealtimeHeadlines,
		ns.GetMarketWatchBulletins,
		ns.GetMarketWatchMarketPulse,
	}

	for _, fetchFunc := range feeds {
		items, err := fetchFunc()
		if err != nil {
			// Log error but continue with other feeds
			continue
		}
		allNews = append(allNews, items...)
	}

	return allNews, nil
}

// fetchRSSFeed is a helper method to fetch and parse any RSS feed
func (ns *NewsService) fetchRSSFeed(url string) ([]NewsItem, error) {
	// Make HTTP request
	resp, err := ns.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch RSS feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse XML
	var feed RSSFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("failed to parse RSS feed: %w", err)
	}

	// Parse pub dates
	for i := range feed.Channel.Items {
		if feed.Channel.Items[i].PubDate != "" {
			// Try to parse RFC1123 format (common in RSS)
			if t, err := time.Parse(time.RFC1123, feed.Channel.Items[i].PubDate); err == nil {
				feed.Channel.Items[i].PublishedAt = t
			} else if t, err := time.Parse(time.RFC1123Z, feed.Channel.Items[i].PubDate); err == nil {
				feed.Channel.Items[i].PublishedAt = t
			}
		}
	}

	return feed.Channel.Items, nil
}

// GetLatestNews returns the most recent N news items
func (ns *NewsService) GetLatestNews(limit int) ([]NewsItem, error) {
	items, err := ns.GetGoogleNews()
	if err != nil {
		return nil, err
	}

	if limit > 0 && limit < len(items) {
		return items[:limit], nil
	}

	return items, nil
}

// FilterNewsByKeywords filters news items by keywords in title or description
func (ns *NewsService) FilterNewsByKeywords(items []NewsItem, keywords []string) []NewsItem {
	if len(keywords) == 0 {
		return items
	}

	filtered := make([]NewsItem, 0)
	for _, item := range items {
		for _, keyword := range keywords {
			if contains(item.Title, keyword) || contains(item.Description, keyword) {
				filtered = append(filtered, item)
				break
			}
		}
	}

	return filtered
}

// Helper function for case-insensitive string matching
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (s[:len(substr)] == substr ||
		s[len(s)-len(substr):] == substr ||
		findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
