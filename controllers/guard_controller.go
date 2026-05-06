package controllers

import (
	"context"
	"net/http"
	"prophet-trader/services"
	"time"

	"github.com/gin-gonic/gin"
)

// GuardController exposes the trade guard state over HTTP.
type GuardController struct {
	guard *services.TradeGuard
}

// NewGuardController creates the controller.
func NewGuardController(guard *services.TradeGuard) *GuardController {
	return &GuardController{guard: guard}
}

// HandleGetStatus returns the current guard configuration and per-agent symbol ownership.
// GET /api/v1/guard/status
func (gc *GuardController) HandleGetStatus(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	status := gc.guard.Status(ctx)
	c.JSON(http.StatusOK, status)
}
