package services

// NewsCleanerService is the interface for AI-powered news cleaning.
// Both ClaudeService and XAIService implement this.
type NewsCleanerService interface {
	CleanNewsForTrading(newsItems []NewsItem) (*CleanedNews, error)
}
