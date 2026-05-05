package client

import (
	"context"
	"github.com/Ricaardo/guanfu/pkg/model"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"
)

type MockClient struct{}

func NewMockClient() *MockClient {
	return &MockClient{}
}

func (m *MockClient) GetSnapshot(ctx context.Context) (*model.MarketSnapshot, error) {
	// 模拟生成一些数据
	// 假设今天是牛市

	now := time.Now()

	// 1. BTC Price: 假设价格在 MA120 上方
	btcPrice := decimal.NewFromFloat(65000.0)

	// 生成 200 天历史价格，呈现上升趋势
	history := make([]decimal.Decimal, 201)
	basePrice := 40000.0
	for i := 0; i <= 200; i++ {
		// 简单线性增长 + 随机波动
		p := basePrice + float64(i)*100 + (rand.Float64()-0.5)*1000
		history[i] = decimal.NewFromFloat(p)
	}
	// 确保当前价格匹配
	history[0] = btcPrice // 注意：通常 index 0 是最新还是最旧？
	// 约定：History[0] 是 T-0 (今天), History[1] 是 T-1 (昨天)...
	// 重新生成符合约定的
	for i := 0; i <= 200; i++ {
		p := 65000.0 - float64(i)*100 + (rand.Float64()-0.5)*1000
		history[i] = decimal.NewFromFloat(p)
	}

	// 2. BTC Volume
	volHistory := make([]decimal.Decimal, 121)
	for i := 0; i <= 120; i++ {
		v := 10000.0 + (rand.Float64()-0.5)*2000
		volHistory[i] = decimal.NewFromFloat(v)
	}

	// 3. Top 50 Coins
	topCoins := make([]model.CoinSnapshot, 50)
	for i := 0; i < 50; i++ {
		// 模拟 70% 的币种在 MA120 上方
		isBull := rand.Float64() < 0.7

		current := decimal.NewFromFloat(10.0)
		hist := make([]decimal.Decimal, 121)

		for d := 0; d <= 120; d++ {
			var p float64
			if isBull {
				// 历史价格比现在低
				p = 10.0 - float64(d)*0.05
			} else {
				// 历史价格比现在高
				p = 10.0 + float64(d)*0.05
			}
			hist[d] = decimal.NewFromFloat(p)
		}

		topCoins[i] = model.CoinSnapshot{
			Symbol:       "COIN",
			Price:        current,
			PriceHistory: hist,
		}
	}

	// 4. Market Cap
	mcapHistory := make([]decimal.Decimal, 91)
	currentCap := 2000000000000.0 // 2T
	for i := 0; i <= 90; i++ {
		// 过去 90 天增长了
		c := currentCap - float64(i)*1000000000
		mcapHistory[i] = decimal.NewFromFloat(c)
	}

	return &model.MarketSnapshot{
		Date:                  now,
		SnapshotSchemaVersion: model.CurrentMarketSnapshotSchemaVersion,
		BTCPrice:              btcPrice,
		BTCVolume24h:          volHistory[0],
		BTCPriceHistory:       history,
		BTCVolumeHistory:      volHistory,
		BTCPriceAsOf:          now.UTC().Format("2006-01-02"),
		Top50Coins:            topCoins,
		TotalMarketCap:        decimal.NewFromFloat(currentCap),
		TotalMarketCapHistory: mcapHistory,
		USDTPriceCNY:          decimal.NewFromFloat(7.3), // 溢价
		USDPriceCNY:           decimal.NewFromFloat(7.1),
		BTCDominance:          decimal.NewFromFloat(0.52), // 52%
		FearGreedIndex:        decimal.NewFromFloat(65),   // Greed
	}, nil
}
