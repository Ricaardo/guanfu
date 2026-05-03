#!/usr/bin/env python3
"""futu_bridge.py — 富途 OpenD JSON 桥接，通过官方 SDK 批量拉取行情。
单次 OpenQuoteContext 拉全部 symbol（避免重复 RSA 握手），输出纯 JSON 到 stdout。
用法: echo '{"symbols":["US.QQQ","US.SPY"],"days":1000}' | python3 futu_bridge.py"""

import sys, json, logging
logging.disable(logging.CRITICAL)

from futu import *

def main():
    inp = json.load(sys.stdin) if not sys.stdin.isatty() else {}
    syms = inp.get('symbols', ['US.QQQ', 'US.SPY'])
    days = min(inp.get('days', 1000), 3000)

    out = {}
    q = OpenQuoteContext('127.0.0.1', 11111)
    try:
        for sym in syms:
            try:
                ret, data, _ = q.request_history_kline(sym, ktype=KLType.K_DAY, max_count=min(days, 1000))
                if ret == RET_OK and not data.empty:
                    closes = data['close'].tolist()
                    closes.reverse()  # newest first
                    out[sym] = {
                        'price': float(closes[0]),
                        'history': closes,
                        'as_of': str(data['time_key'].iloc[-1]),
                    }
                else:
                    out[sym] = {'error': f'RET={ret} empty={data.empty if data is not None else True}'}
            except Exception as e:
                out[sym] = {'error': str(e)}
    finally:
        q.close()

    json.dump(out, sys.stdout, ensure_ascii=False)

if __name__ == '__main__':
    main()
