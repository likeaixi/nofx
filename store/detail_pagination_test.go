package store

import (
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestTraderDetailPaginationRemainsScoped(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&TraderOrder{}, &TraderPosition{}); e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 5; i++ {
		id := "mine"
		if i == 5 {
			id = "other"
		}
		db.Create(&TraderOrder{TraderID: id, ExchangeOrderID: fmt.Sprint(i), Symbol: "BTCUSDT", Side: "BUY", Type: "MARKET", CreatedAt: 100})
		db.Create(&TraderPosition{TraderID: id, Symbol: "BTCUSDT", Side: "LONG", Status: "CLOSED", ExitTime: 100})
	}
	orders, e := NewOrderStore(db).GetTraderOrdersFiltered("mine", "", "", 2, 2)
	if e != nil || len(orders) != 2 || orders[0].ID != 2 || orders[1].ID != 1 {
		t.Fatal(orders, e)
	}
	positions, e := NewPositionStore(db).GetClosedPositionsByTraderFilters([]string{"mine"}, nil, 2, 2)
	if e != nil || len(positions) != 2 || positions[0].ID != 2 || positions[1].ID != 1 {
		t.Fatal(positions, e)
	}
}
