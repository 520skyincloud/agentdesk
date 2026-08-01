package builders

import (
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/services"
)

func BuildStoreWorkbench(snapshot *services.StoreWorkbenchSnapshot) *response.StoreWorkbenchResponse {
	if snapshot == nil {
		return nil
	}
	ret := &response.StoreWorkbenchResponse{
		Bound:       snapshot.Binding != nil,
		TenantID:    snapshot.TenantID,
		TenantName:  snapshot.TenantName,
		UserID:      snapshot.UserID,
		Username:    snapshot.Username,
		Nickname:    snapshot.Nickname,
		Avatar:      snapshot.Avatar,
		ManagedMode: snapshot.Runtime.ManagedMode,
	}
	if snapshot.Binding == nil {
		return ret
	}
	ret.BindingID = snapshot.Binding.ID
	ret.BindingStatus = snapshot.Binding.Status
	ret.StoreID = snapshot.Binding.StoreID
	ret.AgentTeamID = snapshot.Binding.AgentTeamID
	ret.ManagedMode = snapshot.Runtime.ManagedMode
	ret.ServiceHours = snapshot.Runtime.ServiceHours
	ret.StoreRoomConversationID = snapshot.Runtime.StoreRoomConversationID
	ret.StoreRoomNotifyEnabled = snapshot.Runtime.StoreRoomNotifyEnabled
	ret.StoreRoomAtList = snapshot.Runtime.StoreRoomAtList
	ret.FallbackToHQ = snapshot.Runtime.FallbackToHQ
	ret.ManualTimeoutMinutes = snapshot.Runtime.ManualTimeoutMinutes
	ret.UpdatedAt = snapshot.Binding.UpdatedAt.Format("2006-01-02 15:04:05")
	if snapshot.Store != nil {
		ret.StoreCode = snapshot.Store.StoreCode
		ret.StoreName = snapshot.Store.Name
		ret.BrandName = snapshot.Store.BrandName
		ret.StoreAddress = snapshot.Store.Address
		ret.StoreNavigationName = snapshot.Store.NavigationName
		ret.StoreLongitude = snapshot.Store.Longitude
		ret.StoreLatitude = snapshot.Store.Latitude
		ret.StoreMapProvider = snapshot.Store.MapProvider
	}
	if snapshot.AgentTeam != nil {
		ret.AgentTeamName = snapshot.AgentTeam.Name
	}
	if snapshot.KnowledgeBase != nil {
		ret.KnowledgeBaseID = snapshot.KnowledgeBase.ID
		ret.KnowledgeBaseName = snapshot.KnowledgeBase.Name
	}
	if snapshot.WxWorkInstance != nil {
		instance := snapshot.WxWorkInstance
		ret.WxWorkInstanceID = instance.ID
		ret.WxWorkEmployeeID = instance.EmployeeUserID
		ret.WxWorkEmployeeName = instance.EmployeeName
		ret.WxWorkEmployeeAvatar = instance.EmployeeAvatar
		ret.WxWorkHealthStatus = instance.HealthStatus
		if instance.LastHeartbeatAt != nil {
			ret.WxWorkLastHeartbeatAt = instance.LastHeartbeatAt.Format("2006-01-02 15:04:05")
		}
		ret.AIReplyEnabled = instance.AIReplyEnabled
	}
	return ret
}
