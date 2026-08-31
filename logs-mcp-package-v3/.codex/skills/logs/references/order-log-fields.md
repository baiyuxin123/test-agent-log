# Order Log Fields

Use this reference when extracting DSers/DianShi/Shopify order context from SLS logs.

## Projects

| Domain | SLS project |
|---|---|
| DSers related logs | `k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a` |
| 点石 / Shopify / order-name lifecycle logs | `k8s-log-c19589a718db24dbb835525e4e8f2c2a0` |

When an order investigation crosses domains, use the project that owns the starting identifier first, then query the paired project with discovered IDs. Keep the provenance clear in the answer.

Default logstore: `dsers-app`.

## Canonical Query Fields

Use these user-facing meanings before mapping to raw log fields:

| User-facing field | Meaning | Raw aliases to check |
|---|---|---|
| `order_name` | Shopify 的订单名，比如 `#167591` | `order_name`, `name`, `order_no`, `SellerOrderName` |
| `order_sn` | 点石订单号 | `order_sn` |
| `order_id` | DSers 订单 ID | `order_id` only when the surrounding context says DSers, plus `DsersOrderId`, `dsers_order_id` |

Rules:

- Query `order_name`, `order_sn`, and `order_id` explicitly when the user gives an order identifier.
- If the user gives a bare numeric Shopify order name such as `167591`, search it as Shopify `order_name` with the likely `#167591` form as well.
- Do not use Shopify `number` as a substitute for `order_name`.
- Raw `order_id` is overloaded in logs. Preserve the parent path/context before calling it DSers, DianShi, seller, or supplier.
- If `number=167591` but `order_name=#168591`, it is not a match for requested Shopify `order_name=167591`.

## High-signal Terms

| Need | Query terms |
|---|---|
| DSers search index state | `fullInsertData`, `DsersOrderId`, `PlatformOrderStatus` |
| Shopify/source order | `seller_order`, `seller_order_id`, `order_no`, `financial_status`, `fulfillment_status` |
| DianShi order creation | `CreateOrder`, `dsers_order_id`, `order_list`, `supplier_app_id` |
| Price changes | `BatchUpdateOrderPrice`, `ORDER_CHANGE`, `GetOrderQuote`, `GetOrdersDetail` |
| Supplier status | `supplier_order_status`, `ITEM_SUPPLY_STATUS`, `SupplyOrderId`, `SupplySource` |
| Error tracing | exact error text plus ID, then exact `trace_id` |

## Common Fields

| Meaning | Fields |
|---|---|
| Shopify order name | `order_no`, `SellerOrderName`, `order_name`, `name` |
| DianShi / 点石 order_sn | `order_sn` |
| Shopify seller order ID | `seller_order_id`, `ThirdPartyOrderId`, `order_id` in seller logs |
| DSers order ID | `DsersOrderId` |
| DianShi dsers order ID | `dsers_order_id` in `merchant-order-core/CreateOrder` |
| DianShi order ID | `order_list:{order_id:"..."}` |
| Supplier order ID | `SupplyOrderId`, `supplier_order_id` |
| Store ID | `StoreId`, `store_id`, `store_ids` |
| DSers user ID | `UserId`, `user_id` |
| Seller platform app | `seller_app_id` |
| Supplier platform app | `supplier_app_id`, `supplierAppId`, `SupplySource` when nonzero |
| Agent ID | `agency_id`, `agent_id`, `supply_store_id` for Agency supply |

## Status Interpretation

| Raw value | Meaning |
|---|---|
| `ORDER_TAB_STATUS_AWAITING_ORDER` | DSers item awaiting order |
| `ORDER_TAB_STATUS_AWAITING_PAYMENT` | DSers item awaiting payment |
| `ORDER_TAB_STATUS_FULFILLED` | DSers item fulfilled |
| `ORDER_ITEM_STATUS_PAID` | Seller-side item paid |
| `ORDER_ITEM_STATUS_FULFILLED` | Seller-side item fulfilled |
| `ITEM_SUPPLY_STATUS_PLACED_ORDER` | Supplier order placed |
| `supply_source=0` / `SupplySource:0` | `unmapped` |
| `FULFILL_SOURCE_SELL` | Seller-side fulfillment source |
| `supplier_app_id=1658073296948719616` | Agency supplier platform |

## Required Order Answer Checklist

Before answering an order-status question, check whether the response includes:

- Seller-side platform, order number, and status.
- DSers order ID and status.
- DianShi/supplier order ID and status when present.
- Store name, agent ID, DSers user ID, store ID.
- Item title, item ID, item status, supplier/source per item.
- Any discovered trace ID for failures or changes.

If a field is absent after checking the relevant project, mark it as `未在日志中查到` instead of guessing.

## Performance Notes

- Avoid starting with `*` over long windows. Use exact IDs/names and high-signal terms.
- SQL analysis can truncate large `content`; use raw search rows for long payloads.
- Batch logs may contain hundreds of IDs. Verify the target ID's field context before drawing conclusions.
- Some middleware/server logs have empty `trace_id`; use adjacent service logs with the same operation/time/error to find the useful trace.
