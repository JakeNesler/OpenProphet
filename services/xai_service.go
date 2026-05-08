package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// XAIService handles interactions with xAI's Grok API (OpenAI-compatible)
type XAIService struct {
	apiKey     string
	httpClient *http.Client
	model      string
}

// xaiChatRequest is the OpenAI-compatible chat completions request
type xaiChatRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []xaiMessage `json:"messages"`
}

type xaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type xaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// NewXAIService creates a new xAI service. Falls back to XAI_API_KEY env var.
func NewXAIService(apiKey string) *XAIService {
	if apiKey == "" {
		apiKey = os.Getenv("XAI_API_KEY")
	}
	return &XAIService{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		model: "grok-3-mini",
	}
}

// CleanNewsForTrading implements NewsCleanerService using xAI Grok.
func (xs *XAIService) CleanNewsForTrading(newsItems []NewsItem) (*CleanedNews, error) {
	if len(newsItems) == 0 {
		return nil, fmt.Errorf("no news items provided")
	}

	var newsText strings.Builder
	for i, item := range newsItems {
		newsText.WriteString(fmt.Sprintf("[%d] %s\n", i+1, item.Title))
		if item.Description != "" {
			cleanDesc := strings.ReplaceAll(item.Description, "<", "")
			cleanDesc = strings.ReplaceAll(cleanDesc, ">", "")
			newsText.WriteString(fmt.Sprintf("   %s\n", cleanDesc[:minLen(200, len(cleanDesc))]))
		}
		newsText.WriteString(fmt.Sprintf("   Source: %s | Published: %s\n\n", item.Source, item.PubDate))
	}

	systemPrompt := `You are a financial analyst AI. Your role is to analyze news articles and produce concise, structured trading intelligence reports in JSON format. Focus on stock symbols, market sentiment, and actionable trading insights. Always respond with valid JSON only — no markdown, no explanation outside the JSON.`

	userPrompt := fmt.Sprintf(`Analyze the following %d news articles and create a CONCISE trading intelligence report.

NEWS ARTICLES:
%s

Provide a JSON response with this EXACT structure:
{
  "market_sentiment": "BULLISH|BEARISH|NEUTRAL",
  "key_themes": ["theme1", "theme2", "theme3"],
  "stock_mentions": {
    "SYMBOL": "POSITIVE|NEGATIVE|NEUTRAL with 1-sentence reason"
  },
  "actionable_items": ["brief actionable insight 1", "brief actionable insight 2"],
  "executive_summary": "2-3 sentence summary of the market situation"
}

Focus on:
- Stock symbols and their sentiment
- Market-moving themes
- Actionable trading insights
- Overall market direction

Keep it BRIEF and DENSE. Maximum 200 tokens total.`, len(newsItems), newsText.String())

	response, err := xs.generateContent(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	var cleanedNews CleanedNews
	cleanedNews.GeneratedAt = time.Now()
	cleanedNews.SourceCount = countUniqueSources(newsItems)
	cleanedNews.ArticleCount = len(newsItems)
	cleanedNews.FullAnalysis = response

	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		jsonStr := response[jsonStart : jsonEnd+1]
		var parsed struct {
			MarketSentiment  string            `json:"market_sentiment"`
			KeyThemes        []string          `json:"key_themes"`
			StockMentions    map[string]string `json:"stock_mentions"`
			ActionableItems  []string          `json:"actionable_items"`
			ExecutiveSummary string            `json:"executive_summary"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err == nil {
			cleanedNews.MarketSentiment = parsed.MarketSentiment
			cleanedNews.KeyThemes = parsed.KeyThemes
			cleanedNews.StockMentions = parsed.StockMentions
			cleanedNews.ActionableItems = parsed.ActionableItems
			cleanedNews.ExecutiveSummary = parsed.ExecutiveSummary
		}
	}

	return &cleanedNews, nil
}

func (xs *XAIService) generateContent(systemPrompt, userPrompt string) (string, error) {
	reqBody := xaiChatRequest{
		Model:     xs.model,
		MaxTokens: 1024,
		Messages: []xaiMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.x.ai/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+xs.apiKey)

	resp, err := xs.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var xaiResp xaiChatResponse
	if err := json.Unmarshal(body, &xaiResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(xaiResp.Choices) == 0 {
		return "", fmt.Errorf("no content in response")
	}

	return xaiResp.Choices[0].Message.Content, nil
}
