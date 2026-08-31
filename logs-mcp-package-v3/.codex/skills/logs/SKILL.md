---
name: logs
description: Query Alibaba Cloud SLS logs for DSers, DianShi/点石, Shopify, order names, order IDs, order_name/order_sn/order_id, supplier orders, order status changes, price changes, item states, platform mapping, store/user/agent IDs, and supplier-side diagnosis. Use when the user asks to 查日志/查订单状态/查商品/查供端/查 trace/order name/order id/Shopify order id/DSers order id/点石订单号 for DSers order investigations.
---

# Logs

## Core Rules

Use Alibaba Cloud SLS as the source of truth for live log lookups. Network/SLS commands require escalated execution because they access remote cloud logs.

Project routing:

- DSers domain logs: `k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a`
- 点石 / Shopify / order-name lifecycle logs: `k8s-log-c19589a718db24dbb835525e4e8f2c2a0`
- Default logstore: `dsers-app`
- If a user gives an order name or asks for cross-platform order status, start from the domain implied by the identifier, then use discovered IDs to query the paired project. State which project supplied each fact when results differ.

Order identifier field contract - apply this before semantic interpretation:

- `order_name` = Shopify 的订单名，比如 `#167591`.
- `order_sn` = 点石订单号.
- `order_id` = DSers 订单 ID.
- When the user gives an order identifier, query all three fields explicitly: `order_name`, `order_sn`, and `order_id`. Report which field matched.
- If the user gives a bare numeric Shopify order name such as `167591`, search it as Shopify `order_name` with the likely `#167591` form as well.
- Do not guess, normalize, or semantically substitute identifiers across fields. Shopify `number`, UI display numbers, customer order numbers, and internal IDs are not interchangeable with `order_name`.
- If a log row has `number=167591` but `order_name=#168591`, that row is not a match for requested Shopify `order_name=167591`; report the mismatch instead of answering for the wrong order.
- If no exact match is found in the three fields, say not found, list checked fields/time range, and ask the user to provide one of Shopify `order_name`, 点石 `order_sn`, or DSers `order_id`.

When querying order-related issues, always return:

1. Seller-side, DSers, and DianShi/supplier-side order numbers and statuses.
2. Store name, agent ID, DSers user ID, and store ID.
3. Item information and the state of each item.

Interpretation rules:

- Treat `supply_source=0` or `SupplySource:0` as `unmapped`, not Sell.
- Treat `FULFILL_SOURCE_SELL` as seller-side fulfillment source.
- Treat `supplier_app_id=1658073296948719616` as Agency; include `agency_id`, `supply_store_id`, and supply store name when present.
- Keep source uncertainty visible. If only one side is found, state which project was queried and which side was missing.
- Do not expose credentials, cookies, bearer tokens, or raw request headers.

For field details and extraction hints, read [references/order-log-fields.md](references/order-log-fields.md) when performing a real lookup.

## Workflow

1. Identify the input type:
   - Shopify `order_name`, Shopify 的订单名，比如 `#167591`
   - `order_sn`, 点石订单号
   - `order_id`, DSers 订单 ID
   - Shopify seller order ID / platform ID such as `7064153129250`
   - DSers order ID such as `2069688716277841920`
   - DianShi/supplier order ID such as `2070430877621551104`
   - Trace ID or error text

2. Query the right project first:
   - DSers-only logs: query `k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a`.
   - Shopify / 点石 / order-name lifecycle logs: query `k8s-log-c19589a718db24dbb835525e4e8f2c2a0`.
   - DianShi/merchant-order-core order creation, quotes, price changes, supplier order status may require `k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a` after IDs are discovered.
   - If the first project gives only partial context, query the other project with discovered IDs.

3. Use narrow searches before broad scans:
   - Start with exact order name or ID plus a high-signal term: `fullInsertData`, `OrderHook`, `CreateOrder`, `BatchUpdateOrderPrice`, `ORDER_CHANGE`, `supplier_order_status`, `GetAgentOrderLists`.
   - For long payloads, use raw search mode and parse locally instead of relying only on SQL `SELECT content`, because analysis output can truncate large `content`.
   - Once a trace ID is found, query by exact trace to reconstruct the call chain.

4. Extract and reconcile:
   - Seller side: platform, seller order name, seller order ID, financial/fulfillment/item statuses.
   - DSers side: `DsersOrderId`, `UserId`, `StoreId`, `SellerOrderName`, `PlatformOrderStatus`, `OrderStatus`, item states.
   - DianShi/supplier side: `dsers_order_id`, `order_list.order_id`, `supplier_order_id`, `supplier_order_status`, `supplier_app_id`, `agency_id`, `supply_store_id`, supply account name.
   - Items: item ID, product title, variant/SKU, quantity, DSers product ID, platform status, supply source, supply order ID, fulfilled source.

5. Answer in the required business format. Avoid dumping raw logs unless the user explicitly asks for log content.

## Output Format

Use concise Chinese tables by default.

Start with a one-line conclusion when there is a clear answer.

Then include:

### 订单状态

| 平台 | 订单号 | 状态 |
|---|---|---|
| 销端平台 | `...` | `...` |
| DSers | `...` | `...` |
| 点石/供端 | `...` | `...` |

### 店铺/账号

| 字段 | 值 |
|---|---|
| 销端平台 | `...` |
| 销端店铺 | `...` |
| 供端平台 | `...` |
| 供端账号/店铺 | `...` |
| agent id | `...` |
| DSers user_id | `...` |
| DSers store_id | `...` |
| seller_app_id | `...` |
| supplier_app_id | `...` |

### 商品状态

| 商品 | item_id | 状态 | 供端/履约来源 |
|---|---|---|---|
| ... | `...` | `...` | `...` |

For price changes, use:

| 时间 | 操作 | 调整参数 | 调整后结果 | trace_id |
|---|---|---|---|---|

For errors, lead with:

- latest `trace_id`
- failing operation/caller
- exact error reason
- suspected cause based on ID mapping

## Query Notes

Prefer the packaged MCP `sls_query` tool for live SLS lookup. The MCP server fetches SLS credentials from the configured key API at runtime and caches them in memory; do not ask users to configure plaintext credential files.

Use `GetLogsRequest(project, "dsers-app", from_time, to_time, topic="", query=...)`.

Recommended time windows:

- Status/order detail: 2-7 days.
- Recent operation or price change: start with 24 hours, expand if empty.
- Historic unknown order: 14-30 days, but narrow by project and exact terms.

When a broad search returns too much, refine by:

- exact order ID plus operation name
- exact order name plus status/source keyword
- exact trace ID
- discovered DSers order ID or supplier order ID

Do not treat an empty result in one project as final until the correct paired project has also been checked for that domain.
