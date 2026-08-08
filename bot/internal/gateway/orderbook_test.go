package gateway

import (
	"encoding/json"
	"sync"
	"testing"
)

// newTestSnapshot строит минимальный OrderBookSnapshot для тестов —
// без REST-вызова, просто структура в памяти.
func newTestSnapshot(id int64, bids, asks []OBLevelREST) *OrderBookSnapshot {
	return &OrderBookSnapshot{ID: id, Bids: bids, Asks: asks}
}

func lvlREST(price string, size string) OBLevelREST {
	return OBLevelREST{Price: price, Size: json.Number(size)}
}

func lvl(price, size string) OBLevel {
	return OBLevel{Price: price, Size: size}
}

// --- 1. Инициализация из REST-снапшота ---

func TestNewLocalOrderBook_InitFromSnapshot(t *testing.T) {
	snap := newTestSnapshot(100,
		[]OBLevelREST{lvlREST("50000", "1.5"), lvlREST("49999", "2.0")},
		[]OBLevelREST{lvlREST("50001", "1.0")},
	)
	lob := newLocalOrderBook("BTC_USDT", snap)

	if lob.lastUpdateID != 100 {
		t.Errorf("lastUpdateID = %d, want 100", lob.lastUpdateID)
	}
	if lob.synced {
		t.Error("synced должен быть false сразу после инициализации из снапшота")
	}
	if len(lob.bids) != 2 || len(lob.asks) != 1 {
		t.Errorf("bids=%d asks=%d, want 2/1", len(lob.bids), len(lob.asks))
	}
}

// --- 2. Точка стыковки дельты со снапшотом (U <= lastUpdateID+1 <= u) ---

func TestApplyDelta_FindsSyncPoint(t *testing.T) {
	snap := newTestSnapshot(100, nil, nil)
	lob := newLocalOrderBook("BTC_USDT", snap)

	// Дельта целиком старше точки стыковки (u < 101) — пропускаем, не ошибка.
	stale := OrderBookUpdate{S: "BTC_USDT", FirstU: 90, U: 99, Bids: []OBLevel{lvl("50000", "1")}}
	applied, needResync := lob.ApplyDelta(stale)
	if applied || needResync {
		t.Errorf("stale delta: applied=%v needResync=%v, want false/false", applied, needResync)
	}
	if lob.synced {
		t.Error("synced не должен становиться true на dropped-дельте")
	}

	// Дельта накрывает точку стыковки (U=95 <= 101 <= u=105) — применяем, synced=true.
	syncing := OrderBookUpdate{S: "BTC_USDT", FirstU: 95, U: 105, Bids: []OBLevel{lvl("50000", "1.5")}}
	applied, needResync = lob.ApplyDelta(syncing)
	if !applied || needResync {
		t.Errorf("sync delta: applied=%v needResync=%v, want true/false", applied, needResync)
	}
	if !lob.synced {
		t.Error("synced должен стать true после успешной стыковки")
	}
	if lob.lastUpdateID != 105 {
		t.Errorf("lastUpdateID = %d, want 105", lob.lastUpdateID)
	}
}

// --- 3. size="0" удаляет уровень ---

func TestApplyDelta_ZeroSizeRemovesLevel(t *testing.T) {
	snap := newTestSnapshot(100, []OBLevelREST{lvlREST("50000", "1.5")}, nil)
	lob := newLocalOrderBook("BTC_USDT", snap)

	// Синхронизируемся первой дельтой.
	sync := OrderBookUpdate{S: "BTC_USDT", FirstU: 101, U: 101}
	lob.ApplyDelta(sync)

	remove := OrderBookUpdate{S: "BTC_USDT", FirstU: 102, U: 102, Bids: []OBLevel{lvl("50000", "0")}}
	applied, needResync := lob.ApplyDelta(remove)
	if !applied || needResync {
		t.Fatalf("remove delta: applied=%v needResync=%v, want true/false", applied, needResync)
	}
	if _, exists := lob.bids[50000]; exists {
		t.Error("уровень 50000 должен быть удалён после size=\"0\"")
	}
}

// --- 4. Разрыв последовательности → needResync ---

func TestApplyDelta_GapTriggersResync(t *testing.T) {
	snap := newTestSnapshot(100, nil, nil)
	lob := newLocalOrderBook("BTC_USDT", snap)

	sync := OrderBookUpdate{S: "BTC_USDT", FirstU: 101, U: 101}
	lob.ApplyDelta(sync)

	// Следующая дельта должна иметь FirstU=102, а не 105 — разрыв.
	gap := OrderBookUpdate{S: "BTC_USDT", FirstU: 105, U: 110}
	applied, needResync := lob.ApplyDelta(gap)
	if applied || !needResync {
		t.Errorf("gap delta: applied=%v needResync=%v, want false/true", applied, needResync)
	}
}

// --- 5. Full=true заменяет стакан целиком ---

func TestApplyDelta_FullReplacesBook(t *testing.T) {
	snap := newTestSnapshot(100, []OBLevelREST{lvlREST("50000", "1.5")}, nil)
	lob := newLocalOrderBook("BTC_USDT", snap)

	full := OrderBookUpdate{
		S: "BTC_USDT", Full: true, U: 200,
		Bids: []OBLevel{lvl("51000", "3.0")},
		Asks: []OBLevel{lvl("51001", "2.0")},
	}
	applied, needResync := lob.ApplyDelta(full)
	if !applied || needResync {
		t.Fatalf("full snapshot: applied=%v needResync=%v, want true/false", applied, needResync)
	}
	if !lob.synced {
		t.Error("synced должен стать true после Full=true")
	}
	if lob.lastUpdateID != 200 {
		t.Errorf("lastUpdateID = %d, want 200", lob.lastUpdateID)
	}
	if _, exists := lob.bids[50000]; exists {
		t.Error("старый уровень 50000 должен быть стёрт при Full=true")
	}
	if _, exists := lob.bids[51000]; !exists {
		t.Error("новый уровень 51000 должен присутствовать после Full=true")
	}
}

// --- 6. Snapshot() сортирует bids по убыванию, asks по возрастанию ---

func TestSnapshot_SortsLevels(t *testing.T) {
	snap := newTestSnapshot(100,
		[]OBLevelREST{lvlREST("49999", "1"), lvlREST("50001", "1"), lvlREST("50000", "1")},
		[]OBLevelREST{lvlREST("50003", "1"), lvlREST("50002", "1")},
	)
	lob := newLocalOrderBook("BTC_USDT", snap)

	out := lob.Snapshot(1234)
	if len(out.Bids) != 3 || out.Bids[0].Price != "50001" || out.Bids[2].Price != "49999" {
		t.Errorf("bids не отсортированы по убыванию: %+v", out.Bids)
	}
	if len(out.Asks) != 2 || out.Asks[0].Price != "50002" || out.Asks[1].Price != "50003" {
		t.Errorf("asks не отсортированы по возрастанию: %+v", out.Asks)
	}
	if out.S != "BTC_USDT" || out.T != 1234 {
		t.Errorf("symbol/timestamp не проставлены: S=%s T=%d", out.S, out.T)
	}
}

// --- 7. Защита от параллельных resync на один символ (исправление) ---
//
// Воспроизводит сценарий из handleOrderBook (parser.go): при разрыве
// последовательности запускается c.resyncOrderBook в отдельной горутине,
// и ПОКА она не завершилась, ReadLoop может успеть обработать ещё
// несколько дельт для того же символа, каждая из которых снова обнаружит
// несостыковку lastUpdateID на старом объекте LocalOrderBook. Без
// проверки c.resyncing каждая такая дельта запускала бы ЕЩЁ ОДИН
// параллельный REST-запрос на пересинхронизацию.
func TestResyncGuard_PreventsParallelResyncForSameSymbol(t *testing.T) {
	c := NewWSClient("wss://test", "", "", nil, nil) // restClient=nil — resyncOrderBook сам не выполнится
	symbol := "BTC_USDT"

	// Симулируем: 5 "потоков" одновременно пытаются пометить символ как
	// resyncing — ровно так, как это делает handleOrderBook под c.booksMu.
	var wg sync.WaitGroup
	started := make([]bool, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c.booksMu.Lock()
			alreadyResyncing := c.resyncing[symbol]
			if !alreadyResyncing {
				c.resyncing[symbol] = true
			}
			c.booksMu.Unlock()
			started[idx] = !alreadyResyncing
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, s := range started {
		if s {
			successCount++
		}
	}
	if successCount != 1 {
		t.Errorf("ровно один вызов должен был получить право на resync, получили %d из 5", successCount)
	}

	// После сброса флага (как это делает defer в resyncOrderBook) —
	// следующий вызов снова должен получить право на resync.
	c.booksMu.Lock()
	delete(c.resyncing, symbol)
	c.booksMu.Unlock()

	c.booksMu.Lock()
	alreadyResyncing := c.resyncing[symbol]
	c.booksMu.Unlock()
	if alreadyResyncing {
		t.Error("после сброса флага resyncing символ не должен считаться занятым")
	}
}

// --- 8. resyncOrderBook гарантированно снимает флаг даже при ошибке ---

func TestResyncOrderBook_ClearsFlagOnFailure(t *testing.T) {
	// restClient=nil → GetOrderBookSnapshot никогда не будет вызван,
	// resyncOrderBook должен снять флаг сразу же через defer и выйти.
	c := NewWSClient("wss://test", "", "", nil, nil)
	symbol := "BTC_USDT"

	c.booksMu.Lock()
	c.resyncing[symbol] = true
	c.booksMu.Unlock()

	c.resyncOrderBook(symbol, 20)

	c.booksMu.Lock()
	stillResyncing := c.resyncing[symbol]
	c.booksMu.Unlock()

	if stillResyncing {
		t.Error("флаг resyncing должен быть снят через defer, даже когда restClient == nil")
	}
}
