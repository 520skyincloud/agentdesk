package bootstrap

import (
	"agent-desk/internal/handlers/dashboard"

	"github.com/gin-gonic/gin"
)

func registerDashboardPMSSandboxRoutes(group *gin.RouterGroup) {
	group.GET("/stores", dashboard.PMSSandboxGetStores)
	group.GET("/list", dashboard.PMSSandboxGetList)
	group.GET("/customers", dashboard.PMSSandboxGetCustomers)
	group.POST("/initialize", dashboard.PMSSandboxPostInitialize)
	group.POST("/reset", dashboard.PMSSandboxPostReset)
	group.POST("/update", dashboard.PMSSandboxPostUpdate)
	group.POST("/bind", dashboard.PMSSandboxPostBind)
	group.POST("/cancel", dashboard.PMSSandboxPostCancel)
}
