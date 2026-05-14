package controllers

import (
	"net/http"
	"prophet-trader/services"

	"github.com/gin-gonic/gin"
)

// regimeGateStatusSource is the subset of RegimeGateService the controller
// needs. Declared as an interface so the test can supply a fake without
// reaching into the real service's filesystem cache.
type regimeGateStatusSource interface {
	GetStatus() services.RegimeGateStatus
}

// RegimeGateController exposes the regime gate state over HTTP, mirroring
// GuardController. The status this returns is the same shape agents receive
// via the (future) get_regime_gate_status MCP tool.
type RegimeGateController struct {
	gate regimeGateStatusSource
}

// NewRegimeGateController creates the controller.
func NewRegimeGateController(gate regimeGateStatusSource) *RegimeGateController {
	return &RegimeGateController{gate: gate}
}

// HandleGetStatus returns the current regime gate status.
// GET /api/v1/regime-gate/status
//
// Fail-open semantics are inside RegimeGateService.GetStatus — a missing or
// stale file still produces a usable status (tier=UNKNOWN, sizing=1.0, block=false),
// so this handler never errors out.
func (c *RegimeGateController) HandleGetStatus(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, c.gate.GetStatus())
}
