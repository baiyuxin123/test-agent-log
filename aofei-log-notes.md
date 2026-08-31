# AoFei Log Notes

This file records AoFei/傲飞-only log lookup conventions and verified examples.

## Output Rules

- Treat the conversation context as AoFei-only unless the user explicitly asks for another platform.
- Use `kg` for all weight fields.
- For destination, report `country / postal_code`.
- For product rows, include `sku id` when the log has `sku_code`, `sku_id`, `skuId`, or equivalent.
- If `sku id` is absent from the create-order log, write `未在下单日志中返回`.
- Do not mix in DianShi or DSers order-cost interpretations when answering AoFei logistics questions.

## Query Hints

- AoFei logistics logs are currently found through the SLS logstore `dsers-app`.
- Query the exact AoFei order number first, then narrow by operation or error keyword.
- High-signal terms:
  - Create order: `/waybill/create`, `create order success`, `create order fail`
  - Jisu: `jisu api`, `jisu create order`
  - Yuntu: `yuntu create order fail`, `fee-details`, `charge_weight`
  - UI/list related noise: `GetTicket`, `GetRefundOrdersById`
- If exact create-order logs are not found with `/waybill/create`, try `error`, `fail`, `reason`, and logistics-provider-specific terms.

## Verified Cases

### `AF132074748091145257024` - 下单成功

| Field | Value |
|---|---|
| Result | 下单成功 |
| Time | `2026-07-08 14:51:06` |
| Provider | `JisuLogistics` |
| API | `/waybill/create` |
| Channel | `JS11-敏感货` |
| Destination | `FR / 75001` |
| Weight | `0.50 kg` |
| Volume | `1 x 1 x 1` |
| Declared value | `19.20` |
| Waybill | `JSEGD0808428338YQ` |
| Label type | `SENSITIVE` |

Product rows from the create-order log:

| Product | sku id | HS Code | Unit price | Quantity |
|---|---|---|---:|---:|
| 黑色无腿夹鼻老花眼镜... | `未在下单日志中返回` | `71171900` | `4.00` | `2` |
| 黑色无腿夹鼻老花眼镜... | `未在下单日志中返回` | `71171900` | `5.60` | `2` |

Notes:

- The create-order request includes AoFei identifiers: `productionAndSalesEnterpriseName=傲飞`, `productionAndSalesEnterpriseCode=aofei`, `salesPlatform=傲飞平台`.
- Sensitive recipient details were present in logs but should be omitted unless explicitly needed.

### `AF012070039962881228864` - 下单失败

| Field | Value |
|---|---|
| Result | 下单失败 |
| Time | `2026-06-25 15:02:42` |
| Provider | 云途 |
| Product/channel | `BKDDTK` |
| Destination | `CA / J0X 3B0` |
| Province/city | `QC / WAKEFIELD` |
| Weight | `0.525 kg` |
| Error code | `02039162` |
| Error reason | `可到国家验证：当前运输方式选择的目地可到国家、省州或城市或邮编不可到，请根据具体报错内容进行修改或联系客服人员` |

Product rows from `declaration_info`:

| Product | sku id | HS Code | Unit price | Quantity | Unit weight |
|---|---|---|---:|---:|---:|
| 黑色无腿夹鼻老花眼镜... | `12345267891` | `71171900` | `5.6` | `2` | `0.401 kg` |
| 黑色无腿夹鼻老花眼镜... | `12343252Aa` | `71171900` | `4` | `2` | `0.402 kg` |

Conclusion:

- The provider rejected the order because the selected transport method is not serviceable for the destination country/province/city/postal-code combination.
