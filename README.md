# bitflyer-vaccum

約定履歴を全部保存します。

```json
[
  {
    "product_code": "BTC_JPY"
  },
  {
    "product_code": "FX_BTC_JPY"
  },
  {
    "product_code": "ETH_BTC"
  },
  {
    "product_code": "BCH_BTC"
  },
  {
    "product_code": "BTCJPY28DEC2018",
    "alias": "BTCJPY_MAT3M"
  },
  {
    "product_code": "BTCJPY14DEC2018",
    "alias": "BTCJPY_MAT1WK"
  },
  {
    "product_code": "BTCJPY21DEC2018",
    "alias": "BTCJPY_MAT2WK"
  }
]
```

1 リクエスト件数 500

開始: 636061155

1. リクエスト生成
   - beforeID: 636061155 + 1
   - afterID: 636061155 - 500

56,53: 55, 54
54,51: 53, 52
52,49: 51, 50
50,47: 49, 48
