package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/oauth2/google"
)

func main() {
	godotenv.Load()

	saJSON := os.Getenv("GCP_SERVICE_ACCOUNT_JSON")
	billingID := os.Getenv("GCP_BILLING_ACCOUNT_ID")

	if saJSON == "" {
		fmt.Println("❌ GCP_SERVICE_ACCOUNT_JSON 未设置")
		os.Exit(1)
	}
	fmt.Println("✅ GCP_SERVICE_ACCOUNT_JSON 已设置")

	if billingID == "" {
		fmt.Println("❌ GCP_BILLING_ACCOUNT_ID 未设置")
		os.Exit(1)
	}
	fmt.Printf("✅ GCP_BILLING_ACCOUNT_ID = %s\n", billingID)

	// 读取 JSON
	jsonData := []byte(saJSON)
	if !strings.HasPrefix(strings.TrimSpace(saJSON), "{") {
		var err error
		jsonData, err = os.ReadFile(saJSON)
		if err != nil {
			fmt.Printf("❌ 读取 service account 文件失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ 已读取文件: %s (%d bytes)\n", saJSON, len(jsonData))
	} else {
		fmt.Println("✅ 使用内联 JSON 凭据")
	}

	// 解析凭据
	ctx := context.Background()
	creds, err := google.CredentialsFromJSON(ctx, jsonData,
		"https://www.googleapis.com/auth/cloud-billing.readonly",
		"https://www.googleapis.com/auth/cloud-platform",
	)
	if err != nil {
		fmt.Printf("❌ 解析凭据失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ 凭据解析成功")

	// 输出 service account 邮箱
	var sa struct{ ClientEmail string `json:"client_email"` }
	json.Unmarshal(jsonData, &sa)
	if sa.ClientEmail != "" {
		fmt.Printf("📧 Service Account: %s\n", sa.ClientEmail)
	}

	// 获取 token
	token, err := creds.TokenSource.Token()
	if err != nil {
		fmt.Printf("❌ 获取 token 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Token 获取成功, 过期时间: %s\n", token.Expiry.Format(time.RFC3339))

	// 测试 billing API
	url := fmt.Sprintf("https://cloudbilling.googleapis.com/v1/billingAccounts/%s", billingID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		fmt.Printf("❌ 请求 billing API 失败: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 {
		fmt.Printf("✅ Billing API 访问成功: %s\n", string(body))
	} else {
		fmt.Printf("❌ Billing API 返回 %d: %s\n", resp.StatusCode, string(body))
	}

	// 查询 SA 在 project 上的 IAM 权限
	var saInfo struct {
		ProjectID string `json:"project_id"`
	}
	json.Unmarshal(jsonData, &saInfo)
	if saInfo.ProjectID != "" {
		fmt.Printf("\n🔍 检查 Project: %s 的 IAM 权限...\n", saInfo.ProjectID)
		iamURL := fmt.Sprintf("https://cloudresourcemanager.googleapis.com/v1/projects/%s:getIamPolicy", saInfo.ProjectID)
		iamReq, _ := http.NewRequest("POST", iamURL, strings.NewReader("{}"))
		iamReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
		iamReq.Header.Set("Content-Type", "application/json")
		iamResp, err := (&http.Client{Timeout: 15 * time.Second}).Do(iamReq)
		if err != nil {
			fmt.Printf("❌ 查询 IAM 失败: %v\n", err)
		} else {
			defer iamResp.Body.Close()
			iamBody, _ := io.ReadAll(iamResp.Body)
			if iamResp.StatusCode != 200 {
				fmt.Printf("❌ IAM API 返回 %d: %s\n", iamResp.StatusCode, string(iamBody))
			} else {
				// 解析并只显示与此 SA 相关的角色
				var policy struct {
					Bindings []struct {
						Role    string   `json:"role"`
						Members []string `json:"members"`
					} `json:"bindings"`
				}
				json.Unmarshal(iamBody, &policy)
				fmt.Println("📋 该 SA 在 Project 上的角色:")
				found := false
				for _, b := range policy.Bindings {
					for _, m := range b.Members {
						if strings.Contains(m, "gcpasv@") {
							fmt.Printf("   - %s\n", b.Role)
							found = true
						}
					}
				}
				if !found {
					fmt.Println("   (未找到任何角色绑定)")
				}
			}
		}
	}

	// 查询 billing account 列表（看 SA 能看到哪些 billing account）
	fmt.Println("\n🔍 检查 SA 可访问的 Billing Accounts...")
	listURL := "https://cloudbilling.googleapis.com/v1/billingAccounts"
	listReq, _ := http.NewRequest("GET", listURL, nil)
	listReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	listResp, err := (&http.Client{Timeout: 15 * time.Second}).Do(listReq)
	if err != nil {
		fmt.Printf("❌ 查询 billing accounts 失败: %v\n", err)
	} else {
		defer listResp.Body.Close()
		listBody, _ := io.ReadAll(listResp.Body)
		if listResp.StatusCode != 200 {
			fmt.Printf("❌ Billing list API 返回 %d: %s\n", listResp.StatusCode, string(listBody))
		} else {
			fmt.Printf("✅ 可访问的 Billing Accounts: %s\n", string(listBody))
		}
	}

	// 用 testIamPermissions 检查 SA 在 billing account 上的实际权限
	fmt.Println("\n🔍 测试 SA 在 Billing Account 上的具体权限...")
	testURL := fmt.Sprintf("https://cloudbilling.googleapis.com/v1/billingAccounts/%s:testIamPermissions", billingID)
	testPayload := `{"permissions":["billing.accounts.get","billing.accounts.list","billing.accounts.getIamPolicy","billing.budgets.get","billing.credits.list","billing.accounts.getSpendingInformation"]}`
	testReq, _ := http.NewRequest("POST", testURL, strings.NewReader(testPayload))
	testReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	testReq.Header.Set("Content-Type", "application/json")
	testResp, err := (&http.Client{Timeout: 15 * time.Second}).Do(testReq)
	if err != nil {
		fmt.Printf("❌ testIamPermissions 失败: %v\n", err)
	} else {
		defer testResp.Body.Close()
		testBody, _ := io.ReadAll(testResp.Body)
		fmt.Printf("📋 testIamPermissions 返回 %d: %s\n", testResp.StatusCode, string(testBody))
	}

	// 尝试获取 billing account 的 IAM 策略
	fmt.Println("\n🔍 获取 Billing Account IAM 策略...")
	policyURL := fmt.Sprintf("https://cloudbilling.googleapis.com/v1/billingAccounts/%s:getIamPolicy", billingID)
	policyReq, _ := http.NewRequest("POST", policyURL, strings.NewReader("{}"))
	policyReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	policyReq.Header.Set("Content-Type", "application/json")
	policyResp, err := (&http.Client{Timeout: 15 * time.Second}).Do(policyReq)
	if err != nil {
		fmt.Printf("❌ getIamPolicy 失败: %v\n", err)
	} else {
		defer policyResp.Body.Close()
		policyBody, _ := io.ReadAll(policyResp.Body)
		fmt.Printf("📋 IAM Policy 返回 %d: %s\n", policyResp.StatusCode, string(policyBody))
	}

	// 测试各种 Cost API 端点
	fmt.Println("\n🔍 测试各种 Cost API 端点...")
	costURLs := []struct{ name, url string }{
		{"v1beta1 costs:summarize", fmt.Sprintf("https://cloudbilling.googleapis.com/v1beta1/billingAccounts/%s/services/-/costs:summarize", billingID)},
		{"v1beta costSummary", fmt.Sprintf("https://cloudbilling.googleapis.com/v1beta/billingAccounts/%s/costSummary", billingID)},
		{"v1 budgets list", fmt.Sprintf("https://billingbudgets.googleapis.com/v1/billingAccounts/%s/budgets", billingID)},
		{"v1 projects list", fmt.Sprintf("https://cloudbilling.googleapis.com/v1/billingAccounts/%s/projects", billingID)},
		{"v1 services list", "https://cloudbilling.googleapis.com/v1/services?pageSize=5"},
		{"v2beta anomalies", fmt.Sprintf("https://cloudbilling.googleapis.com/v2beta/billingAccounts/%s/anomalies", billingID)},
	}
	for _, c := range costURLs {
		r, _ := http.NewRequest("GET", c.url, nil)
		r.Header.Set("Authorization", "Bearer "+token.AccessToken)
		rr, err := (&http.Client{Timeout: 15 * time.Second}).Do(r)
		if err != nil {
			fmt.Printf("   ❌ %s: %v\n", c.name, err)
			continue
		}
		defer rr.Body.Close()
		b, _ := io.ReadAll(rr.Body)
		s := string(b)
		if len(s) > 500 {
			s = s[:500] + "..."
		}
		fmt.Printf("   %s → %d: %s\n", c.name, rr.StatusCode, s)
	}
}
