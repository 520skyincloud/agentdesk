package dashboard

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func BillingQueryPostOptions(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionBillingView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.BillingQueryService.Options(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, data)
}

func BillingQueryPostGet(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionBillingView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.BillingQueryRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.BillingQueryService.Query(ctx.Request.Context(), req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, data)
}

func BillingQueryPostExport(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionBillingExport)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.BillingQueryRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.BillingQueryService.QueryExport(ctx.Request.Context(), req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ctx.Header("Content-Type", "text/csv; charset=utf-8")
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="model-billing-%s-%s.csv"`, data.StartDate, data.EndDate))
	ctx.Status(http.StatusOK)
	_, _ = ctx.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(ctx.Writer)
	_ = writer.Write([]string{
		"证据类型", "接入公司", "门店", "Request ID", "模型", "阶段/状态", "输入 Token", "输出 Token",
		"缓存 Token", "Quota", "人民币金额", "发生时间", "Model Profile Revision", "Credential Revision", "说明",
	})
	writeBillingOfficialCSV(writer, data)
	writeBillingLocalCSV(writer, data)
	writeBillingReconciliationCSV(writer, data)
	writer.Flush()
}

func writeBillingOfficialCSV(writer *csv.Writer, data *response.BillingQueryResponse) {
	for _, store := range data.Official.Stores {
		description := fmt.Sprintf(
			"总额度=%d; 已使用=%d; 可用=%d; 已使用人民币=%.6f; 可用人民币=%.6f",
			store.Summary.TotalGranted, store.Summary.TotalUsed, store.Summary.TotalAvailable,
			store.Summary.UsedCNY, store.Summary.AvailableCNY,
		)
		if store.ErrorMessage != "" {
			description = store.ErrorMessage
		}
		_ = writer.Write([]string{
			"NewAPI 官方额度汇总", billingCSVCell(store.TenantName), billingCSVCell(store.StoreName), "",
			billingCSVCell(strings.Join(store.ModelNames, ", ")), billingCSVCell(store.Status), "", "", "",
			strconv.FormatInt(store.Summary.TotalUsed, 10), formatBillingCSVFloat(store.Summary.UsedCNY), "",
			strconv.FormatInt(store.ModelProfileRevision, 10), strconv.FormatInt(store.CredentialRevision, 10), billingCSVCell(description),
		})
		_ = writer.Write([]string{
			"NewAPI 官方期间汇总", billingCSVCell(store.TenantName), billingCSVCell(store.StoreName), "",
			billingCSVCell(strings.Join(store.ModelNames, ", ")), billingCSVCell(store.Status),
			strconv.FormatInt(store.PeriodPromptTokens, 10), strconv.FormatInt(store.PeriodOutputTokens, 10), "",
			strconv.FormatInt(store.PeriodQuota, 10), formatBillingCSVFloat(store.PeriodCostCNY), "",
			strconv.FormatInt(store.ModelProfileRevision, 10), strconv.FormatInt(store.CredentialRevision, 10), billingCSVCell(store.ErrorClass),
		})
		for _, item := range store.Logs {
			_ = writer.Write([]string{
				"NewAPI 官方调用", billingCSVCell(store.TenantName), billingCSVCell(store.StoreName), billingCSVCell(item.RequestID),
				billingCSVCell(item.ModelName), "official", strconv.FormatInt(item.PromptTokens, 10), strconv.FormatInt(item.CompletionTokens, 10), "",
				strconv.FormatInt(item.Quota, 10), formatBillingCSVFloat(item.CostCNY), formatBillingCSVUnix(item.CreatedAt),
				strconv.FormatInt(store.ModelProfileRevision, 10), strconv.FormatInt(store.CredentialRevision, 10), "",
			})
		}
	}
}

func writeBillingLocalCSV(writer *csv.Writer, data *response.BillingQueryResponse) {
	for _, item := range data.Local.Events {
		_ = writer.Write([]string{
			"本地 Usage", billingCSVCell(item.TenantName), billingCSVCell(item.StoreName), billingCSVCell(item.RequestID),
			billingCSVCell(item.ModelName), billingCSVCell(firstBillingCSVValue(item.Stage, item.Status)),
			strconv.FormatInt(item.PromptTokens, 10), strconv.FormatInt(item.CompletionTokens, 10), strconv.FormatInt(item.CachedPromptTokens, 10),
			"", "", item.CreatedAt.Format(time.DateTime), strconv.FormatInt(item.ModelProfileRevision, 10),
			strconv.FormatInt(item.CredentialRevision, 10), billingCSVCell(item.ErrorClass),
		})
	}
}

func writeBillingReconciliationCSV(writer *csv.Writer, data *response.BillingQueryResponse) {
	storeTenant := make(map[int64]string)
	for _, store := range data.Official.Stores {
		storeTenant[store.StoreID] = store.TenantName
	}
	for _, item := range data.Reconciliation.Items {
		occurredAt := ""
		if item.OfficialAt != nil {
			occurredAt = item.OfficialAt.Format(time.DateTime)
		} else if item.LocalAt != nil {
			occurredAt = item.LocalAt.Format(time.DateTime)
		}
		description := fmt.Sprintf("官方Token=%d; 本地Token=%d", item.OfficialTokens, item.LocalTokens)
		_ = writer.Write([]string{
			"Request ID 对账", billingCSVCell(storeTenant[item.StoreID]), billingCSVCell(item.StoreName), billingCSVCell(item.RequestID),
			billingCSVCell(firstBillingCSVValue(item.OfficialModel, item.LocalModel)), billingCSVCell(item.Status), "", "", "", "",
			formatBillingCSVFloat(item.OfficialCostCNY), occurredAt, "", "", billingCSVCell(description),
		})
	}
}

func formatBillingCSVFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func formatBillingCSVUnix(value int64) string {
	if value <= 0 {
		return ""
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return time.Unix(value, 0).In(location).Format(time.DateTime)
}

func billingCSVCell(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.ContainsRune("=+-@", rune(value[0])) {
		return "'" + value
	}
	return value
}

func firstBillingCSVValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
