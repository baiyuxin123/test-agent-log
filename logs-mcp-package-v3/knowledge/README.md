# Logs Knowledge

This folder mirrors the business rules embedded in the `logs` skill and MCP resources.

## Projects

| Domain | SLS project |
|---|---|
| DSers domain logs | `k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a` |
| 点石 / Shopify / 傲飞 / order-name lifecycle logs | `k8s-log-c19589a718db24dbb835525e4e8f2c2a0` |

Default logstore: `dsers-app`.

## Required Order Answer

For order-related questions, always list:

1. Seller-side / DSers / 点石 or supplier-side order numbers and statuses.
2. Store name, agent ID, DSers user ID, and store ID.
3. Product/item information and the state of each item.

## Important Interpretation Rules

- `supply_source=0` or `SupplySource:0` means `unmapped`.
- `FULFILL_SOURCE_SELL` means seller-side fulfillment source.
- `supplier_app_id=1658073296948719616` means Agency.
- Keep missing fields explicit as `未在日志中查到`; do not guess.
- Do not expose credentials, cookies, bearer tokens, or raw request headers.

## Query Strategy

- Start with exact identifiers and high-signal terms.
- Query `order_name`, `order_sn`, and `order_id` explicitly before interpreting a user-supplied order identifier.
- Use paired projects when an order crosses DSers and 点石/Shopify domains.
- For long payloads, prefer raw search rows because SQL analysis can truncate `content`.
- After finding a trace ID, query by exact trace ID to reconstruct the call chain.

See `order-log-fields.md` for field aliases, status interpretation, and extraction hints.
