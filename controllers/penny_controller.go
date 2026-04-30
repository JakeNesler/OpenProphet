package controllers

import (
	"net/http"
	"strconv"

	"prophet-trader/services"

	"github.com/gin-gonic/gin"
)

// PennyController handles penny stock signal HTTP requests.
type PennyController struct {
	aggregator *services.PennySignalAggregator
}

// NewPennyController creates the controller.
func NewPennyController(aggregator *services.PennySignalAggregator) *PennyController {
	return &PennyController{aggregator: aggregator}
}

// HandleGetCandidates returns scored penny stock candidates above a minimum score.
// GET /api/v1/penny/candidates?min_score=60[&detail=true]
//
// By default the response omits per-candidate context strings (technical_context,
// regulatory_event, social_context) to keep the list payload small — call
// GET /api/v1/penny/signal/:ticker to retrieve full context for a single ticker.
// Pass detail=true to include all context strings inline (legacy/debug behavior).
func (pc *PennyController) HandleGetCandidates(c *gin.Context) {
	minScoreStr := c.DefaultQuery("min_score", "60")
	minScore, err := strconv.ParseFloat(minScoreStr, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid min_score"})
		return
	}
	detail := c.Query("detail") == "true"
	var candidates []services.CandidateScore
	if detail {
		candidates = pc.aggregator.GetCandidates(minScore)
	} else {
		candidates = pc.aggregator.GetCandidateSummaries(minScore)
	}
	c.JSON(http.StatusOK, gin.H{
		"count":      len(candidates),
		"min_score":  minScore,
		"candidates": candidates,
	})
}

// HandleGetSignalDetail returns the full signal breakdown for one ticker.
// GET /api/v1/penny/signal/:ticker
func (pc *PennyController) HandleGetSignalDetail(c *gin.Context) {
	ticker := c.Param("ticker")
	detail := pc.aggregator.GetSignalDetail(ticker)
	if detail == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ticker not tracked", "ticker": ticker})
		return
	}
	c.JSON(http.StatusOK, detail)
}

// HandleGetUniverse returns the current monitored universe.
// GET /api/v1/penny/universe
func (pc *PennyController) HandleGetUniverse(c *gin.Context) {
	universe := pc.aggregator.GetUniverse()
	c.JSON(http.StatusOK, gin.H{
		"count":    len(universe),
		"universe": universe,
	})
}

// HandleScanNow triggers an immediate universe refresh.
// POST /api/v1/penny/scan
func (pc *PennyController) HandleScanNow(c *gin.Context) {
	pc.aggregator.RefreshUniverse()
	c.JSON(http.StatusOK, gin.H{"status": "refreshing"})
}
