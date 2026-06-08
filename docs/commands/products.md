# 产品与产品组 API

产品和产品组是多数业务命令的定位对象。产品命令使用 `product_id_or_name`，产品组命令使用 `product_group_id_or_name`。

## `get product-list`

获取当前 workspace 下的产品列表。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `fields` | string[]/string | 否 | CLI 返回字段；默认 `id`、`name`、`start_date`、`label` |
| `limit` | integer | 否 | CLI 返回条数上限 |
| `raw` | boolean | 否 | 为 `true` 时保留服务端原始字段 |
| `format` | string | 否 | `json` 或 `ndjson` |

返回：`data.products[]` 为产品列表，`data.total` 为服务端产品数量。支持 `format:"ndjson"`。

示例：

```powershell
rqamsc get product-list --payload '{"fields":["id","name","start_date"],"limit":20,"format":"ndjson"}'
```

## `get product`

获取单个产品详情。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `product_id_or_name` | string | 是 | 产品 ID/名称 |

返回：`data` 为产品详情，字段见[产品字段](#产品字段)。

示例：

```powershell
rqamsc get product --payload '{"product_id_or_name":"demo"}'
```

## `insert product`

创建产品。payload 顶层直接传产品字段。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 产品名称 |
| `report_name` | string | 是 | 对外报告名称 |
| `start_date` | string/date | 是 | 产品开始日期 |
| `trading_start_date` | string/date | 是 | 产品开始交易日期 |
| `data_source` | string | 是 | 数据来源，见[产品枚举字段](#产品枚举字段) |
| `investment_category` | string | 是 | 投资类型 |
| `strategy_category` | string | 是 | 策略类型 |
| `benchmark` | object | 是 | 基准配置 |
| `calendar` | string | 是 | 日历类型 |
| `accounts` | object[] | 是 | 账户配置 |
| `fee_settings` | object | 是 | 费率配置 |
| 其他产品字段 | mixed | 否 | 见[产品字段](#产品字段) |

返回：`data` 为服务端创建结果。

示例：

```powershell
rqamsc insert product --payload '{"name":"demo","report_name":"demo","start_date":"2026-01-01","trading_start_date":"2026-01-01","data_source":"trade","investment_category":"equity","strategy_category":"stock_long","benchmark":{"type":"index","id":"000300.XSHG"},"calendar":"exchange","accounts":[{"name":"stock","is_custodian":false,"account_number":"...","broker":"ricequant"}],"fee_settings":{"management_fee":0,"custodian_fee":0,"operation_fee":0,"sales_and_service_fee":0}}'
```

## `update product`

更新产品字段。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `product_id_or_name` | string | 是 | 产品 ID/名称 |
| `update_fields` | object | 是 | 要更新的产品字段 |

返回：`data` 为更新结果。

示例：

```powershell
rqamsc update product --payload '{"product_id_or_name":"demo","update_fields":{"description":"updated"}}'
```

## `delete product`

删除产品。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `product_id_or_name` | string | 是 | 产品 ID/名称 |

返回：`data` 为删除结果。

## 产品字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 产品 ID |
| `name` | string | 产品名称 |
| `full_name` | string | 产品全名 |
| `report_name` | string | 对外报告名称 |
| `start_date` | string/date | 产品开始日期 |
| `trading_start_date` | string/date | 产品开始交易日期 |
| `data_source` | string | 数据来源 |
| `investment_category` | string | 投资类型 |
| `strategy_category` | string | 策略类型 |
| `benchmark` | object | 基准配置 |
| `calendar` | string | 产品日历 |
| `accounts[]` | array | 账户配置 |
| `accounts[].name` | string | 账户名称 |
| `accounts[].is_custodian` | boolean | 是否托管账户 |
| `accounts[].account_number` | string | 资金账号 |
| `accounts[].broker` | string | 交易通道 |
| `fee_settings` | object | 费率配置 |
| `fee_settings.management_fee` | number | 管理费率 |
| `fee_settings.custodian_fee` | number | 托管费率 |
| `fee_settings.operation_fee` | number | 运营费率 |
| `fee_settings.sales_and_service_fee` | number | 销售服务费率 |
| `fee_settings.performance_pay` | number | 业绩报酬费率 |
| `realtime_period_type` | string | 实时估值时间类型 |
| `valuation_settings` | object/null | 资产估值方式配置 |
| `exchange_rate_settings` | object/null | 估值汇率配置 |
| `label` | string | 产品标签 |
| `auto_equity` | boolean/null | 是否自动权益 |
| `auto_overwrite` | boolean/null | 是否用估值表自动覆盖头寸 |
| `unit_policy` | string/null | 份额管理方式 |
| `create_time` | string/datetime | 创建时间 |
| `manager` | string/null | 管理人 |
| `invest_advisor` | string/null | 投资顾问 |
| `invest_manager` | string/null | 投资经理 |
| `maturity_date` | string/date/null | 到期日 |
| `closing_date` | string/date/null | 封账日 |
| `fund_code` | string/null | 基金代码 |
| `description` | string | 描述 |
| `product_state` | string | 产品状态 |
| `user_id` | string/int | 创建者用户 ID |
| `workspace_id` | string | 所属 workspace ID |

## 产品枚举字段

| 字段 | 常用取值 |
| --- | --- |
| `data_source` | `trade`、`valuation_report`、`trade_and_valuation_report` |
| `calendar` | `exchange`、`natural` |
| `label` | `live`、`paper`、`paper_trading` |

## `get product-group-list`

获取当前 workspace 下的产品组列表。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `fields` | string[]/string | 否 | CLI 返回字段；默认 `id`、`name`、`start_date`、`label` |
| `limit` | integer | 否 | CLI 返回条数上限 |
| `raw` | boolean | 否 | 为 `true` 时保留服务端原始字段 |
| `format` | string | 否 | `json` 或 `ndjson` |

返回：`data.product_groups[]` 为产品组列表，`data.total` 为服务端产品组数量。支持 `format:"ndjson"`。

## `get product-group`

获取单个产品组详情。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `product_group_id_or_name` | string | 是 | 产品组 ID/名称 |

返回：`data` 为产品组详情，字段见[产品组字段](#产品组字段)。

## `update product-group`

更新产品组字段。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `product_group_id_or_name` | string | 是 | 产品组 ID/名称 |
| `update_fields` | object | 是 | 要更新的产品组字段 |

返回：`data` 为更新结果。

## `delete product-group`

删除产品组。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `product_group_id_or_name` | string | 是 | 产品组 ID/名称 |

返回：`data` 为删除结果。

## 产品组字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 产品组 ID |
| `name` | string | 产品组名称 |
| `report_name` | string | 对外报告名称 |
| `products[]` | array | 产品组内产品列表 |
| `product_ids[]` | array | 产品 ID 列表 |
| `product_weights` | object | 产品权重 |
| `is_agg_by_weights` | boolean | 是否按权重聚合 |
| `benchmark` | object | 产品组基准 |
| `label` | string | 产品组标签 |
| `start_date` | string/date | 估值起始日 |
| `trading_start_date` | string/date | 交易起始日 |
| `realtime_period_type` | string | 实时估值时间类型 |
| `strategy_category` | string[] | 策略类型列表 |
| `rebalance_frequency` | string/null | 再平衡频率 |
| `create_time` | string/datetime | 创建时间 |
| `maturity_date` | string/date/null | 到期日 |
| `description` | string | 产品组描述 |
| `accessible` | boolean | 是否可访问 |
| `accessible_err_msg[]` | array | 访问异常信息 |
| `user_id` | string/int | 创建者用户 ID |
| `workspace_id` | string | 所属 workspace ID |
