#!/usr/bin/env python3
"""AkShare bridge for guanfu — fetches CSI300 history + PE/PB valuation.

Input (stdin JSON):
  {"mode": "history", "symbol": "000300", "days": 5000}
  {"mode": "valuation", "symbol": "000300"}

Output (stdout JSON):
  {"000300": {"price": 4000, "history": [...], "as_of": "2026-05-05", "points": [{"date": "...", "close": ...}]}}
"""

import sys
import json
import akshare as ak
from datetime import datetime

def fetch_history(symbol, days):
    """Fetch CSI300 index daily K-line."""
    try:
        # stock_zh_index_daily: CSI300 = "sh000300"
        code = f"sh{symbol}" if symbol == "000300" else f"sz{symbol}"
        df = ak.stock_zh_index_daily(symbol=code)
        if df is None or len(df) == 0:
            return {"error": "empty dataframe"}

        df = df.sort_values("date")
        if days and len(df) > days:
            df = df.tail(days)

        closes = df["close"].tolist()
        dates = df["date"].tolist()
        points = []
        for i in range(len(dates)):
            d = str(dates[i])[:10]  # YYYY-MM-DD
            points.append({"date": d, "close": float(closes[i])})

        return {
            "price": float(closes[-1]),
            "history": [float(c) for c in closes],
            "as_of": str(dates[-1])[:10],
            "points": points,
            "count": len(points),
        }
    except Exception as e:
        return {"error": str(e)}

def fetch_valuation(symbol):
    """Fetch CSI300 PE/PB history."""
    try:
        # index_value_name_funddb: CSI300 PE/PB
        df = ak.index_value_name_funddb(
            symbol="000300",
            indicator="市盈率",
        )
        if df is None or len(df) == 0:
            return {"error": "empty PE dataframe"}

        pe_points = []
        for _, row in df.iterrows():
            d = str(row["日期"])[:10]
            v = float(row["市盈率"])
            pe_points.append({"date": d, "close": v})

        # Also get PB
        df_pb = ak.index_value_name_funddb(
            symbol="000300",
            indicator="市净率",
        )
        pb_points = []
        if df_pb is not None and len(df_pb) > 0:
            for _, row in df_pb.iterrows():
                d = str(row["日期"])[:10]
                v = float(row["市净率"])
                pb_points.append({"date": d, "close": v})

        return {
            "pe_points": pe_points,
            "pb_points": pb_points,
            "pe_latest": pe_points[-1]["close"] if pe_points else 0,
            "pb_latest": pb_points[-1]["close"] if pb_points else 0,
        }
    except Exception as e:
        return {"error": str(e)}

def fetch_hs300_macro(series):
    """Fetch one HS300-related macro series. Returns oldest-first points list.

    series ∈ { "pmi", "m2", "lpr", "cny", "northbound", "volume", "cpi", "retail" }
    """
    try:
        if series == "pmi":
            df = ak.macro_china_pmi()
            # 月份 = "2026年04月份", 制造业-指数 is the headline value
            df = df[["月份", "制造业-指数"]].rename(columns={"月份": "date", "制造业-指数": "close"})
        elif series == "m2":
            df = ak.macro_china_money_supply()
            df = df[["月份", "货币和准货币(M2)-同比增长"]].rename(
                columns={"月份": "date", "货币和准货币(M2)-同比增长": "close"})
        elif series == "lpr":
            df = ak.macro_china_lpr()
            cols = list(df.columns)
            date_col = next((c for c in cols if c == "TRADE_DATE" or c == "date" or "日期" in c), cols[0])
            lpr1y_col = next((c for c in cols if "LPR1Y" in c or "1年" in c), None)
            df = df[[date_col, lpr1y_col]].rename(columns={date_col: "date", lpr1y_col: "close"})
        elif series == "cny":
            # akshare USD/CNY endpoint shifts often; defer to Yahoo CNY=X via Go fetcher.
            return {"error": "use yahoo CNY=X via YahooETFSource (storage key hs300_cny)"}
        elif series == "northbound":
            df = ak.stock_hsgt_hist_em(symbol="北向资金")
            df = df[["日期", "当日成交净买额"]].rename(
                columns={"日期": "date", "当日成交净买额": "close"})
            # Drop NaN rows (recent data not yet published / market holidays).
            df = df.dropna(subset=["close"])
        elif series == "volume":
            df = ak.stock_zh_index_daily(symbol="sh000300")
            df = df[["date", "volume"]].rename(columns={"volume": "close"})
        elif series == "cpi":
            df = ak.macro_china_cpi_yearly()
            # Same shape as PMI yearly: 商品 / 日期 / 今值 / 预测值 / 前值
            df = df[["日期", "今值"]].rename(columns={"日期": "date", "今值": "close"})
        elif series == "retail":
            df = ak.macro_china_retail_price_index()
            cols = list(df.columns)
            # Layout: 月份 / 零售商品_当月 / 零售商品_累计 / ...; pick first numeric column.
            date_col = next((c for c in cols if c == "月份" or "时间" in c or "date" in c.lower()), cols[0])
            value_col = None
            for c in cols[1:]:
                # heuristic: first non-text column
                if "当月" in c or "本月" in c:
                    value_col = c
                    break
            if value_col is None:
                value_col = cols[1]
            df = df[[date_col, value_col]].rename(columns={date_col: "date", value_col: "close"})
        else:
            return {"error": f"unknown series: {series}"}

        df = df.dropna()
        df["close"] = df["close"].astype(str).str.rstrip("%").astype(float)

        def fmt_date(d):
            s = str(d).strip()
            # Normalize akshare date forms:
            #   "2026-04-01" → unchanged
            #   "2026年04月份" → "2026-04-01"
            #   "2026年4月" → "2026-04-01"
            #   "202604"   → "2026-04-01"
            if "年" in s:
                s = s.replace("年", "-").replace("月", "-01").replace("份", "").replace("日", "")
            if len(s) == 6 and s.isdigit():
                s = f"{s[:4]}-{s[4:6]}-01"
            if len(s) == 7:
                s = s + "-01"
            # zero-pad month: "2026-4-01" → "2026-04-01"
            parts = s.split("-")
            if len(parts) == 3:
                y, m, dd = parts
                if len(m) == 1:
                    m = "0" + m
                if len(dd) == 1:
                    dd = "0" + dd
                # truncate to YYYY-MM-DD if extra
                s = f"{y[:4]}-{m[:2]}-{dd[:2]}"
            return s[:10]

        df["date"] = df["date"].apply(fmt_date)
        df = df.sort_values("date")

        points = [{"date": r["date"], "close": float(r["close"])} for _, r in df.iterrows()]
        return {"points": points, "count": len(points)}
    except Exception as e:
        return {"error": str(e)}


def main():
    stdin = sys.stdin.read()
    if not stdin:
        # Default: fetch CSI300 full history
        result = fetch_history("000300", 6000)
        print(json.dumps({"000300": result}, ensure_ascii=False))
        return

    try:
        args = json.loads(stdin)
    except:
        args = {}

    mode = args.get("mode", "history")
    symbol = args.get("symbol", "000300")
    days = args.get("days", 6000)

    if mode == "valuation":
        result = fetch_valuation(symbol)
        print(json.dumps({"valuation": result}, ensure_ascii=False))
    elif mode == "hs300_macro":
        series = args.get("series", "")
        result = fetch_hs300_macro(series)
        print(json.dumps({series: result}, ensure_ascii=False))
    else:
        result = fetch_history(symbol, days)
        print(json.dumps({symbol: result}, ensure_ascii=False))

if __name__ == "__main__":
    main()
