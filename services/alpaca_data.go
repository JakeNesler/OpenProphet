package services

import (
	"context"
	"fmt"
	"prophet-trader/interfaces"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/v3/marketdata"
	"github.com/sirupsen/logrus"
)

// AlpacaDataService implements DataService using Alpaca Market Data API
type AlpacaDataService struct {
	client *marketdata.Client
	logger *logrus.Logger
}

// NewAlpacaDataService creates a new Alpaca data service
func NewAlpacaDataService(apiKey, secretKey string) *AlpacaDataService {
	client := marketdata.NewClient(marketdata.ClientOpts{
		APIKey:    apiKey,
		APISecret: secretKey,
	})

	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return &AlpacaDataService{
		client: client,
		logger: logger,
	}
}

// GetHistoricalBars retrieves historical bar data
func (s *AlpacaDataService) GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error) {
	s.logger.WithFields(logrus.Fields{
		"symbol":    symbol,
		"start":     start,
		"end":       end,
		"timeframe": timeframe,
	}).Info("Fetching historical bars")

	// Convert timeframe string to Alpaca TimeFrame
	tf := s.parseTimeframe(timeframe)

	req := marketdata.GetBarsRequest{
		TimeFrame:  tf,
		Start:      start,
		End:        end,
		Feed:       marketdata.IEX,
		PageLimit:  10000, // Max allowed
		Adjustment: marketdata.All,
	}

	barsResp, err := s.client.GetBars(symbol, req)
	if err != nil {
		s.logger.WithError(err).Error("Failed to fetch historical bars")
		return nil, fmt.Errorf("failed to get historical bars: %w", err)
	}

	bars := make([]*interfaces.Bar, 0)
	for _, bar := range barsResp {
		bars = append(bars, &interfaces.Bar{
			Symbol:    symbol,
			Timestamp: bar.Timestamp,
			Open:      bar.Open,
			High:      bar.High,
			Low:       bar.Low,
			Close:     bar.Close,
			Volume:    int64(bar.Volume),
			VWAP:      bar.VWAP,
		})
	}

	s.logger.WithField("count", len(bars)).Info("Fetched historical bars")
	return bars, nil
}

// GetMultiBars retrieves historical bar data for multiple symbols in a single request.
// Returns a map keyed by symbol. Missing or errored symbols are simply absent from the map.
func (s *AlpacaDataService) GetMultiBars(ctx context.Context, symbols []string, start, end time.Time, timeframe string) (map[string][]*interfaces.Bar, error) {
	s.logger.WithFields(logrus.Fields{
		"symbols_count": len(symbols),
		"start":         start,
		"end":           end,
		"timeframe":     timeframe,
	}).Info("Fetching multi-symbol historical bars")

	tf := s.parseTimeframe(timeframe)

	req := marketdata.GetBarsRequest{
		TimeFrame:  tf,
		Start:      start,
		End:        end,
		Feed:       marketdata.IEX,
		PageLimit:  10000,
		Adjustment: marketdata.All,
	}

	resp, err := s.client.GetMultiBars(symbols, req)
	if err != nil {
		s.logger.WithError(err).Error("Failed to fetch multi-symbol historical bars")
		return nil, fmt.Errorf("failed to get multi bars: %w", err)
	}

	out := make(map[string][]*interfaces.Bar, len(resp))
	for symbol, bars := range resp {
		converted := make([]*interfaces.Bar, 0, len(bars))
		for _, bar := range bars {
			converted = append(converted, &interfaces.Bar{
				Symbol:    symbol,
				Timestamp: bar.Timestamp,
				Open:      bar.Open,
				High:      bar.High,
				Low:       bar.Low,
				Close:     bar.Close,
				Volume:    int64(bar.Volume),
				VWAP:      bar.VWAP,
			})
		}
		out[symbol] = converted
	}

	s.logger.WithField("symbols_returned", len(out)).Info("Fetched multi-symbol historical bars")
	return out, nil
}

// GetLatestBar retrieves the most recent bar for a symbol
func (s *AlpacaDataService) GetLatestBar(ctx context.Context, symbol string) (*interfaces.Bar, error) {
	req := marketdata.GetLatestBarRequest{
		Feed: marketdata.IEX,
	}

	barsResp, err := s.client.GetLatestBars([]string{symbol}, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest bar: %w", err)
	}

	if bar, ok := barsResp[symbol]; ok {
		return &interfaces.Bar{
			Symbol:    symbol,
			Timestamp: bar.Timestamp,
			Open:      bar.Open,
			High:      bar.High,
			Low:       bar.Low,
			Close:     bar.Close,
			Volume:    int64(bar.Volume),
			VWAP:      bar.VWAP,
		}, nil
	}

	return nil, fmt.Errorf("no bar data found for symbol: %s", symbol)
}

// GetLatestQuote retrieves the most recent quote for a symbol
func (s *AlpacaDataService) GetLatestQuote(ctx context.Context, symbol string) (*interfaces.Quote, error) {
	req := marketdata.GetLatestQuoteRequest{}

	quotesResp, err := s.client.GetLatestQuotes([]string{symbol}, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest quote: %w", err)
	}

	if quote, ok := quotesResp[symbol]; ok {
		return &interfaces.Quote{
			Symbol:    symbol,
			BidPrice:  quote.BidPrice,
			BidSize:   int64(quote.BidSize),
			AskPrice:  quote.AskPrice,
			AskSize:   int64(quote.AskSize),
			Timestamp: quote.Timestamp,
		}, nil
	}

	return nil, fmt.Errorf("no quote data found for symbol: %s", symbol)
}

// GetLatestTrade retrieves the most recent trade for a symbol
func (s *AlpacaDataService) GetLatestTrade(ctx context.Context, symbol string) (*interfaces.Trade, error) {
	req := marketdata.GetLatestTradeRequest{}

	tradesResp, err := s.client.GetLatestTrades([]string{symbol}, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest trade: %w", err)
	}

	if trade, ok := tradesResp[symbol]; ok {
		return &interfaces.Trade{
			Symbol:    symbol,
			Price:     trade.Price,
			Size:      int64(trade.Size),
			Timestamp: trade.Timestamp,
		}, nil
	}

	return nil, fmt.Errorf("no trade data found for symbol: %s", symbol)
}

// StreamBars starts streaming bar data for specified symbols
func (s *AlpacaDataService) StreamBars(ctx context.Context, symbols []string) (<-chan *interfaces.Bar, error) {
	// This would require websocket connection setup
	// For now, returning a simple implementation
	// You can expand this to use Alpaca's streaming API

	barChan := make(chan *interfaces.Bar)

	s.logger.WithField("symbols", symbols).Info("Streaming bars not fully implemented yet")

	// TODO: Implement actual streaming using Alpaca websocket
	// For now, return empty channel
	go func() {
		defer close(barChan)
		<-ctx.Done()
	}()

	return barChan, nil
}

// parseTimeframe converts string timeframe to Alpaca TimeFrame
func (s *AlpacaDataService) parseTimeframe(tf string) marketdata.TimeFrame {
	switch tf {
	case "1Min":
		return marketdata.OneMin
	case "5Min":
		return marketdata.NewTimeFrame(5, marketdata.Min)
	case "15Min":
		return marketdata.NewTimeFrame(15, marketdata.Min)
	case "30Min":
		return marketdata.NewTimeFrame(30, marketdata.Min)
	case "1Hour":
		return marketdata.OneHour
	case "4Hour":
		return marketdata.NewTimeFrame(4, marketdata.Hour)
	case "1Day":
		return marketdata.OneDay
	case "1Week":
		return marketdata.OneWeek
	case "1Month":
		return marketdata.OneMonth
	default:
		return marketdata.OneDay
	}
}
