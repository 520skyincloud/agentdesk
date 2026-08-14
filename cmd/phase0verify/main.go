// Command phase0verify 在服务器上做只读的模型协议验证：
// 确认 DeepSeek Responses API 下 strict json_schema 与 function_call 能否共存。
// 密钥只存于进程内存，不打印、不落盘、不进命令历史。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"agent-desk/internal/pkg/securex"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type credRow struct {
	TenantID            int64
	StoreID             int64
	StoreStaffBindingID int64
	EncryptedKey        string
	KeyNonce            string
	CipherVersion       string
	CredentialRevision  int64
}

func main() {
	masterKey := strings.TrimSpace(os.Getenv("MASTER_KEY"))
	dsn := strings.TrimSpace(os.Getenv("DSN"))
	baseURL := strings.TrimSpace(os.Getenv("BASE_URL"))
	model := strings.TrimSpace(os.Getenv("MODEL"))
	if baseURL == "" {
		baseURL = "http://36.138.68.47:6081/v1"
	}
	if model == "" {
		model = "deepseek-v4-flash"
	}
	if masterKey == "" || dsn == "" {
		fmt.Println("RESULT: missing MASTER_KEY or DSN")
		os.Exit(1)
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Println("RESULT: db open failed:", err)
		os.Exit(1)
	}

	var rows []credRow
	if err := db.Table("t_store_model_credential").
		Where("store_staff_binding_id = ? AND status = ?", 1, "active").
		Scan(&rows).Error; err != nil {
		fmt.Println("RESULT: query failed:", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("RESULT: no active credential for binding 1")
		os.Exit(1)
	}

	cipher, err := securex.NewAESGCM(masterKey)
	if err != nil {
		fmt.Println("RESULT: cipher init failed:", err)
		os.Exit(1)
	}
	row := rows[0]
	aad := credAAD(row.CipherVersion, row.TenantID, row.StoreID, row.StoreStaffBindingID, row.CredentialRevision)
	apiKey, err := cipher.Decrypt(row.EncryptedKey, row.KeyNonce, aad)
	if err != nil {
		fmt.Println("RESULT: decrypt failed:", err)
		os.Exit(1)
	}

	schema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	tools := []map[string]any{{
		"type":        "function",
		"name":        "get_weather",
		"description": "查询天气",
		"parameters":  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"],"additionalProperties":false}`),
	}}

	fmt.Println("RESULT: model=" + model + " binding=1 status=active revision=" + fmt.Sprint(row.CredentialRevision))

	run("共存(tools+json_schema)", baseURL, model, apiKey, "合肥今天天气如何", &tools, &schema)
	run("仅tools", baseURL, model, apiKey, "合肥今天天气如何", &tools, nil)
	run("仅json_schema", baseURL, model, apiKey, "合肥今天天气如何", nil, &schema)
}

func credAAD(version string, tenantID, storeID, bindingID, revision int64) []byte {
	if version == securex.AESGCMCipherVersion+"-binding-v1" {
		return []byte(fmt.Sprintf("tenant:%d:store:%d:binding:%d:revision:%d", tenantID, storeID, bindingID, revision))
	}
	return []byte(fmt.Sprintf("tenant:%d:store:%d:revision:%d", tenantID, storeID, revision))
}

func run(label, baseURL, model, apiKey, input string, tools *[]map[string]any, schema *json.RawMessage) {
	body := map[string]any{
		"model":        model,
		"instructions": "你只做协议验证。",
		"input":        input,
	}
	if tools != nil {
		body["tools"] = *tools
		body["tool_choice"] = "auto"
	}
	if schema != nil {
		body["text"] = map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "phase0",
				"strict": true,
				"schema": *schema,
			},
		}
	}
	payload, _ := json.Marshal(body)

	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/responses", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("RESULT: %s -> request error: %v\n", label, err)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var parsed struct {
		Status     string `json:"status"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	_ = json.Unmarshal(raw, &parsed)

	hasToolCall := false
	toolName := ""
	for _, o := range parsed.Output {
		if o.Type == "function_call" {
			hasToolCall = true
			toolName = o.Name
		}
	}
	// DeepSeek 把文本放在 output[].content[].text；output_text 顶层字段可能为空。
	finalText := strings.TrimSpace(parsed.OutputText)
	if finalText == "" {
		for _, o := range parsed.Output {
			for _, c := range o.Content {
				if strings.TrimSpace(c.Text) != "" {
					finalText += c.Text
				}
			}
		}
	}
	outputValidJSON := json.Valid([]byte(strings.TrimSpace(finalText)))

	fmt.Printf("RESULT: %s -> http=%d status=%s hasFunctionCall=%v toolName=%q outputTextIsValidJSON=%v outputText=%q\n",
		label, resp.StatusCode, parsed.Status, hasToolCall, toolName, outputValidJSON, truncate(finalText, 80))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
