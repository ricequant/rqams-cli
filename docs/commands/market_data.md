# 市场数据 API

本页覆盖市场数据类只读命令。

## `get trading-dates`

查询交易所交易日历，对应服务端 `GET /api/rqams/v2/market_data/trading_dates`。

Payload:

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `type` | string | 是 | 交易日历类型；目前仅支持 `exchange` |
| `start_date` | string | 否 | 开始日期，`YYYY-MM-DD`；不传时服务端默认 `2010-01-01` |
| `end_date` | string | 否 | 结束日期，`YYYY-MM-DD`；不传则返回 `start_date` 之后全部交易日 |
| `fmt` | string | 否 | 服务端返回格式：`date` 或 `timestamp`；默认 `date` |
| `limit` | integer | 否 | CLI 返回条数上限 |
| `format` | string | 否 | CLI 输出格式：`json` 或 `ndjson` |

返回：`data[]` 为交易日历。`fmt:"date"` 时返回 `YYYY-MM-DD` 字符串；`fmt:"timestamp"` 时返回时间戳。

示例：

```powershell
rqamsc get trading-dates --payload '{"type":"exchange","start_date":"2026-01-01","end_date":"2026-01-31"}'
rqamsc get trading-dates --payload '{"type":"exchange","start_date":"2026-01-01","limit":5,"format":"ndjson"}'
```
