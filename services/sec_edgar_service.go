package services

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/html/charset"
)

const regulatoryRefreshInterval = 30 * time.Second
const regulatoryHalfLifeHours = 24.0

type regulatoryEntry struct {
	Entry     DecayEntry
	EventDesc string
}

// dilutionEntry records a recent dilution-related SEC filing on a universe ticker.
// One entry per ticker in dilutionBlocks; replaced when a more conservative
// (takedown beats shelf) filing arrives.
type dilutionEntry struct {
	Ticker    string
	FormType  string    // "S-1", "S-3", "424B5", "8-K-3.02", etc.
	FiledAt   time.Time // best-effort from feed timestamp
	Bucket    string    // "takedown" (2-day) or "shelf" (5-day)
	SourceURL string    // EDGAR filing URL (for log audit trail)
}

const (
	dilutionTakedownWindowDays = 2
	dilutionShelfWindowDays    = 5
)

// SECEdgarService polls EDGAR and GlobeNewswire for regulatory events.
type SECEdgarService struct {
	httpClient    *http.Client
	universe      *PennyUniverseService
	operatorEmail string
	mu            sync.RWMutex
	entries       map[string]regulatoryEntry // keyed by ticker; keeps highest-score entry
	logger        *logrus.Logger
	nowFunc       func() time.Time
	earnings      *EarningsCalendarService

	dilutionMu     sync.RWMutex
	dilutionBlocks map[string]dilutionEntry
}

// NewSECEdgarService creates the service. The earnings parameter provides
// access to the cached Alpaca trading calendar (via earnings.Calendar()) for
// trading-day eviction in the dilution filter; pass nil only in tests that do
// not exercise dilution-filter eviction.
func NewSECEdgarService(
	universe *PennyUniverseService,
	httpClient *http.Client,
	operatorEmail string,
	earnings *EarningsCalendarService,
) *SECEdgarService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &SECEdgarService{
		httpClient:     httpClient,
		universe:       universe,
		operatorEmail:  operatorEmail,
		entries:        make(map[string]regulatoryEntry),
		logger:         logger,
		nowFunc:        time.Now,
		earnings:       earnings,
		dilutionBlocks: make(map[string]dilutionEntry),
	}
}

// Start runs the polling loop until ctx is cancelled.
func (s *SECEdgarService) Start(ctx context.Context) {
	s.poll()
	ticker := time.NewTicker(regulatoryRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll()
		}
	}
}

// GetRegulatoryScore returns the current decayed regulatory score and event description.
func (s *SECEdgarService) GetRegulatoryScore(ticker string) (float64, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[ticker]
	if !ok {
		return 0, ""
	}
	return e.Entry.EffectiveScore(), e.EventDesc
}

func (s *SECEdgarService) poll() {
	tickers := tickerSet(s.universe.GetTickers())
	fb1, tot1 := s.pollEdgar(tickers)
	fb2, tot2 := s.pollGlobeNewswire(tickers)
	total := tot1 + tot2
	fallbacks := fb1 + fb2
	if total > 0 && float64(fallbacks)/float64(total) > 0.50 {
		s.logger.WithField("pct", fmt.Sprintf("%.0f%%", float64(fallbacks)/float64(total)*100)).
			Error("decay anchor fallback rate — EDGAR feed format may have changed")
	}
}

// atomFeed is a minimal ATOM feed parser.
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string `xml:"title"`
	Updated string `xml:"updated"`
	Summary string `xml:"summary"`
}

// rssFeed is a minimal RSS 2.0 feed parser.
type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

func (s *SECEdgarService) fetchAtom(url string) ([]atomEntry, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("PennyProphet Trading Bot %s", s.operatorEmail))
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	var feed atomFeed
	dec := xml.NewDecoder(resp.Body)
	dec.CharsetReader = charset.NewReaderLabel
	if err := dec.Decode(&feed); err != nil {
		return nil, fmt.Errorf("atom parse: %w", err)
	}
	return feed.Entries, nil
}

func (s *SECEdgarService) fetchRSS(url string) ([]rssItem, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("PennyProphet Trading Bot %s", s.operatorEmail))
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	var feed rssFeed
	dec := xml.NewDecoder(resp.Body)
	dec.CharsetReader = charset.NewReaderLabel
	if err := dec.Decode(&feed); err != nil {
		return nil, fmt.Errorf("rss parse: %w", err)
	}
	return feed.Channel.Items, nil
}

// edgarDateLayouts is the ordered list of timestamp formats accepted on
// EDGAR feed entries. Most-likely formats are tried first. Includes
// no-seconds RFC1123 variants (e.g. "Fri, 08 May 2026 17:12 GMT") that
// EDGAR has been observed to emit in 2026.
var edgarDateLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 02 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04 MST",
	"Mon, 02 Jan 2006 15:04 -0700",
}

func tryParseEdgarDate(s string) (time.Time, bool) {
	for _, layout := range edgarDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, false
		}
	}
	return time.Now(), true
}

func parseAtomDate(s string) (time.Time, bool) {
	return tryParseEdgarDate(s)
}

func parseRSSDate(s string) (time.Time, bool) {
	return tryParseEdgarDate(s)
}

func (s *SECEdgarService) pollEdgar(tickers map[string]bool) (fallbacks, total int) {
	const edgarURL = "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=8-K&dateb=&owner=include&count=40&search_text=&output=atom"
	entries, err := s.fetchAtom(edgarURL)
	if err != nil {
		s.logger.WithError(err).Warn("SECEdgarService: EDGAR poll failed")
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		total++
		ticker := extractTickerFromTitle(entry.Title, tickers)
		if ticker == "" {
			continue
		}
		eventTime, isFallback := parseAtomDate(entry.Updated)
		if isFallback {
			fallbacks++
			s.logger.Warnf("decay anchor: skipping %s — unparseable timestamp %q", ticker, entry.Updated)
			continue
		}
		desc := fmt.Sprintf("8-K filed %s", eventTime.Format("15:04 ET"))
		s.upsertEntry(ticker, 40.0, eventTime, desc)
	}
	return fallbacks, total
}

func (s *SECEdgarService) pollGlobeNewswire(tickers map[string]bool) (fallbacks, total int) {
	const gnwURL = "https://www.globenewswire.com/RssFeed/country/US"
	items, err := s.fetchRSS(gnwURL)
	if err != nil {
		s.logger.WithError(err).Warn("SECEdgarService: GlobeNewswire poll failed")
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		eventTime, isFallback := parseRSSDate(item.PubDate)
		combined := strings.ToUpper(item.Title + " " + item.Description)
		for ticker := range tickers {
			if !strings.Contains(combined, ticker) {
				continue
			}
			total++
			if isFallback {
				fallbacks++
				s.logger.Warnf("decay anchor: skipping %s — unparseable timestamp %q", ticker, item.PubDate)
				continue
			}
			desc := fmt.Sprintf("PR wire mention %s", eventTime.Format("15:04 ET"))
			s.upsertEntry(ticker, 25.0, eventTime, desc)
		}
	}
	return fallbacks, total
}

// upsertEntry implements the max rule: replace only when new_base > existing decayed score.
// Caller must hold mu.Lock.
func (s *SECEdgarService) upsertEntry(ticker string, newBase float64, eventTime time.Time, desc string) {
	existing, ok := s.entries[ticker]
	if !ok {
		s.entries[ticker] = regulatoryEntry{
			Entry:     DecayEntry{BaseScore: newBase, EventTime: eventTime, HalfLifeHrs: regulatoryHalfLifeHours},
			EventDesc: desc,
		}
		return
	}
	if newBase > existing.Entry.EffectiveScore() {
		s.entries[ticker] = regulatoryEntry{
			Entry:     DecayEntry{BaseScore: newBase, EventTime: eventTime, HalfLifeHrs: regulatoryHalfLifeHours},
			EventDesc: desc,
		}
	}
}

// extractTickerFromTitle finds a universe ticker in an EDGAR entry title.
// EDGAR 8-K titles look like: "8-K - ACME CORP (0001234567) (Issuer)"
func extractTickerFromTitle(title string, tickers map[string]bool) string {
	upper := strings.ToUpper(title)
	for ticker := range tickers {
		if strings.Contains(upper, " "+ticker+" ") ||
			strings.Contains(upper, "("+ticker+")") ||
			strings.HasSuffix(upper, " "+ticker) ||
			strings.HasPrefix(upper, ticker+" ") {
			return ticker
		}
	}
	return ""
}

// tickerSet converts a slice of ticker strings into a set (map) for O(1) lookup.
func tickerSet(tickers []string) map[string]bool {
	set := make(map[string]bool, len(tickers))
	for _, t := range tickers {
		set[t] = true
	}
	return set
}

// IsDilutionBlocked returns (true, reason) if the ticker has an unexpired
// dilution block, or (false, "") otherwise. Eviction is lazy: an expired
// entry is removed on read.
//
// Fail-closed semantics: if the trading calendar is unavailable (empty), the
// block is preserved rather than dropped. This is the safe direction for a
// capital-protection filter — we'd rather over-suppress than miss a real
// dilution event during a calendar outage.
func (s *SECEdgarService) IsDilutionBlocked(ticker string) (bool, string) {
	s.dilutionMu.RLock()
	entry, ok := s.dilutionBlocks[ticker]
	s.dilutionMu.RUnlock()
	if !ok {
		return false, ""
	}

	var calendar []AlpacaCalendarEntry
	if s.earnings != nil {
		calendar = s.earnings.Calendar()
	}
	if len(calendar) == 0 {
		// Fail-closed: keep blocking when we can't compute eviction.
		return true, dilutionReason(entry, -1)
	}

	now := s.nowFunc()
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	filedDate := time.Date(entry.FiledAt.Year(), entry.FiledAt.Month(), entry.FiledAt.Day(), 0, 0, 0, 0, entry.FiledAt.Location())
	distance := tradingDayDistance(filedDate, nowDate, calendar)
	window := dilutionTakedownWindowDays
	if entry.Bucket == "shelf" {
		window = dilutionShelfWindowDays
	}
	if distance > window {
		s.dilutionMu.Lock()
		// Re-check identity: a concurrent upsertDilutionBlock could have
		// replaced this entry with a fresh filing between our RUnlock above
		// and the Lock here. Only delete if we still see the same stale entry.
		if cur, ok := s.dilutionBlocks[ticker]; ok && cur.FiledAt.Equal(entry.FiledAt) && cur.FormType == entry.FormType {
			delete(s.dilutionBlocks, ticker)
		}
		s.dilutionMu.Unlock()
		return false, ""
	}
	return true, dilutionReason(entry, distance)
}

// dilutionReason builds a human-readable string for log lines and
// log_decision audit trails. distance < 0 means "calendar unavailable".
func dilutionReason(e dilutionEntry, distance int) string {
	if distance < 0 {
		return fmt.Sprintf("%s %s filing (calendar unavailable)", e.FormType, e.Bucket)
	}
	return fmt.Sprintf("%s %s filed %d trading days ago", e.FormType, e.Bucket, distance)
}

// dilutionFormSpec maps an EDGAR `type=` query parameter to the bucket label
// and a human-readable form-type tag for log lines. Order matters only for
// log clarity — multiple specs can match the same atom feed entry but each
// fetch covers exactly one type= value.
type dilutionFormSpec struct {
	queryType string // EDGAR getcurrent type= parameter
	bucket    string // "takedown" or "shelf"
}

// dilutionFormSpecs is the canonical fan-out list for pollDilutionForms.
var dilutionFormSpecs = []dilutionFormSpec{
	{queryType: "S-1", bucket: "takedown"},
	{queryType: "S-3", bucket: "shelf"},
	{queryType: "424", bucket: "takedown"},
	{queryType: "F-1", bucket: "takedown"},
	{queryType: "F-3", bucket: "shelf"},
}

// applyDilutionFiling fetches one EDGAR atom feed for the given type, walks
// each entry, and records a dilution block for any entry whose title contains
// a universe ticker. Used both by the production poll loop and by unit tests
// (which point url at an httptest server serving a fixture).
func (s *SECEdgarService) applyDilutionFiling(formType, bucket, url string, tickers map[string]bool) {
	entries, err := s.fetchAtom(url)
	if err != nil {
		s.logger.WithError(err).WithField("form", formType).
			Warn("SECEdgarService: dilution poll failed for form")
		return
	}
	for _, entry := range entries {
		ticker := extractTickerFromTitle(entry.Title, tickers)
		if ticker == "" {
			continue
		}
		filedAt, isFallback := parseAtomDate(entry.Updated)
		if isFallback {
			s.logger.Warnf("dilution block: skipping %s — unparseable timestamp %q", ticker, entry.Updated)
			continue
		}
		// FormType uses the title's actual form (e.g. "S-3/A") when extractable
		// for log fidelity; falls back to the queried form type otherwise.
		actualForm := extractFormFromTitle(entry.Title, formType)
		s.upsertDilutionBlock(ticker, actualForm, bucket, filedAt, "")
	}
}

// extractFormFromTitle pulls the actual form type from an EDGAR title like
// "S-3/A - ABCD CORP (0001234567) (Filer)". Falls back to the queried form if
// the title doesn't follow the expected leading-form-token pattern.
func extractFormFromTitle(title, fallback string) string {
	upper := strings.ToUpper(title)
	for _, candidate := range []string{"S-1/A", "S-1", "S-3/A", "S-3", "F-1/A", "F-1", "F-3/A", "F-3", "424B2", "424B3", "424B4", "424B5"} {
		if strings.HasPrefix(upper, candidate+" ") || strings.HasPrefix(upper, candidate+"-") {
			return candidate
		}
	}
	return fallback
}

// upsertDilutionBlock writes a dilution entry, applying the replacement rule:
// takedown beats shelf (never downgrade); same bucket replaces (refreshes
// window); shelf does not replace an existing takedown.
func (s *SECEdgarService) upsertDilutionBlock(ticker, formType, bucket string, filedAt time.Time, sourceURL string) {
	s.dilutionMu.Lock()
	defer s.dilutionMu.Unlock()
	existing, ok := s.dilutionBlocks[ticker]
	if ok && existing.Bucket == "takedown" && bucket == "shelf" {
		return // Don't downgrade.
	}
	s.dilutionBlocks[ticker] = dilutionEntry{
		Ticker:    ticker,
		FormType:  formType,
		FiledAt:   filedAt,
		Bucket:    bucket,
		SourceURL: sourceURL,
	}
	s.logger.WithFields(logrus.Fields{
		"ticker":   ticker,
		"form":     formType,
		"bucket":   bucket,
		"filed_at": filedAt.Format(time.RFC3339),
		"source":   sourceURL,
	}).Warn("dilution block created")
}
