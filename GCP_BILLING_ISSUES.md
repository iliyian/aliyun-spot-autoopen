# GCP Credits 监控 — 已知问题与后续计划

## 当前状态

GCP Credits 费用查询功能**暂不可用**，原因如下。

## 问题 1：Billing Account 权限（已解决 ✅）

Service Account 需要在 **Billing Account 层面**单独授权，Project Owner 角色不会自动继承到 Billing Account。

**解决方法**：在 GCP Console → Billing → Account management 中，给 SA 添加 `Billing Account Viewer` 角色。

## 问题 2：Cost API 已下线（未解决 ❌）

代码中使用的 `v1beta1/billingAccounts/{id}/services/-/costs:summarize` 端点已被 Google 完全下线，返回 404。

经过验证，Cloud Billing API 的所有版本（v1、v1beta、v1beta1）均**没有**查询实际费用/消费金额的端点。这些 API 只提供：
- 账户信息管理（v1）
- SKU 价格查询（v1beta）

## 唯一替代方案：BigQuery Billing Export

GCP 查询实际费用的唯一官方方式是 **BigQuery Billing Export**。

### 需要手动操作的步骤

1. 打开 https://console.cloud.google.com/billing/export
2. 选择 "BigQuery export" → "Standard usage cost"
3. 选择目标 project 和 dataset
4. 保存配置
5. **等待数小时到一天**，数据才会开始写入 BigQuery

> ⚠️ 此步骤**无法通过 API 自动化**，必须在 Console 手动完成。

### 后续代码改动

配置完 BigQuery Export 后，需要：

1. 在 `.env` 中新增配置项：
   ```
   GCP_BIGQUERY_PROJECT=your-project-id
   GCP_BIGQUERY_DATASET=billing_export
   GCP_BIGQUERY_TABLE=gcp_billing_export_v1_XXXXXX_XXXXXX_XXXXXX
   ```

2. 添加 `cloud.google.com/go/bigquery` 依赖

3. 改写 `internal/gcp/billing.go` 中的 `queryTotalCost()` 方法，使用 BigQuery SQL 查询：
   ```sql
   SELECT SUM(cost) + SUM(IFNULL((SELECT SUM(c.amount) FROM UNNEST(credits) c), 0))
   FROM `project.dataset.table`
   WHERE billing_account_id = 'XXXXXX-XXXXXX-XXXXXX'
   ```

## 检查工具

`cmd/check_gcp/main.go` 可用于验证 SA 权限：

```bash
go run ./cmd/check_gcp/
```

会检查：凭据解析、Token 获取、Billing API 访问、IAM 权限、可访问的 Billing Accounts 列表。
