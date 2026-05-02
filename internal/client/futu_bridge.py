#!/usr/bin/env python3
"""futu_bridge.py — 富途 OpenD JSON 桥接，通过官方 SDK 拉取行情。
输出纯 JSON 到 stdout。用法: echo '{"symbols":[...],"days":1000}' | python3 futu_bridge.py"""

import sys, json, logging
logging.disable(logging.CRITICAL)  # kill futu SDK debug noise

from futu import *

def fetch(sym, days):
    """拉取 sym 的日K线，返回 dict 或 error"""
    q = OpenQuoteContext('127.0.0.1', 11111)
    try:
        ret, data, key = q.request_history_kline(sym, ktype=KLType.K_DAY, max_count=min(days, 1000))
        if ret != RET_OK or data.empty:
            return {'error': f'RET={ret}'}
        closes = data['close'].tolist()
        closes.reverse()  # newest first
        return {'price': float(closes[-1]), 'history': closes, 'as_of': str(data['time_key'].iloc[-1])}
    except Exception as e:
        return {'error': str(e)}
    finally:
        q.close()

def main():
    inp = json.load(sys.stdin) if not sys.stdin.isatty() else {}
    syms = inp.get('symbols', ['US.QQQ', 'US.SPY'])
    days = inp.get('days', 1000)
    out = {s: fetch(s, days) for s in syms}
    json.dump(out, sys.stdout, ensure_ascii=False)

if __name__ == '__main__':
    main()
