// Package trading owns the durable business state around the matching engine.
package trading

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Side string

const (
	BuyYes  Side = "buy_yes"
	BuyNo   Side = "buy_no"
	SellYes Side = "sell_yes"
	SellNo  Side = "sell_no"
)

type Status string

const (
	Pending     Status = "pending"
	Open        Status = "open"
	Partial     Status = "partial"
	Filled      Status = "filled"
	Cancelled   Status = "cancelled"
	ResolvedYes Status = "resolved_yes"
	ResolvedNo  Status = "resolved_no"
)
const (
	EventMarket   = "market"
	EventBalance  = "balance"
	EventOrder    = "order"
	EventPosition = "position"
	EventTrade    = "trade"
)

var ErrNotMarketOwner = errors.New("only the market owner can resolve it")
var ErrRecoveryRequired = errors.New("durable state uncertain; restart API and engine to recover")

const maxWALRecord = 64 * 1024 * 1024

// A newline commits one complete business batch. Legacy single-event records
// remain readable, but new writes never expose a prefix of a transaction.
type walRecord struct {
	Version int     `json:"version"`
	Events  []Event `json:"events"`
}

type Market struct {
	ID      string `json:"id"`
	Symbol  string `json:"symbol"`
	OwnerID string `json:"owner_id,omitempty"`
	Status  Status `json:"status"`
}
type Order struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	MarketID       string `json:"market_id"`
	Side           Side   `json:"side"`
	Price          int    `json:"price"`
	Quantity       int    `json:"quantity"`
	FilledQuantity int    `json:"filled_quantity"`
	EnginePrice    int    `json:"engine_price"`
	EngineSide     string `json:"engine_side"`
	EngineID       uint32 `json:"engine_id"`
	Status         Status `json:"status"`
	Sequence       uint64 `json:"sequence"`
}
type Trade struct {
	MarketID    string `json:"market_id"`
	BuyOrderID  string `json:"buy_order_id"`
	SellOrderID string `json:"sell_order_id"`
	Price       int32  `json:"price"`
	Quantity    uint32 `json:"quantity"`
}
type Event struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	Type     string `json:"type"`
	Market   Market `json:"market,omitempty"`
	Order    Order  `json:"order,omitempty"`
	Trade    Trade  `json:"trade,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	MarketID string `json:"market_id,omitempty"`
	Amount   int64  `json:"amount,omitempty"`
	YesDelta int    `json:"yes_delta,omitempty"`
	NoDelta  int    `json:"no_delta,omitempty"`
}
type EngineTrade struct {
	BuyOrderID, SellOrderID uint32
	Price                   int32
	Quantity                uint32
}
type EngineResult struct {
	OrderID uint32
	Trades  []EngineTrade
}
type Engine interface {
	AddBook(context.Context, string) error
	AddOrder(context.Context, string, int32, uint32, string) (EngineResult, error)
	RemoveOrder(context.Context, string, uint32) (bool, error)
}
type Projector interface {
	Apply(context.Context, Event) error
}
type BatchProjector interface {
	ApplyBatch(context.Context, []Event) error
}

// Stage names are fixed so instrumentation never creates per-user or per-market labels.
type Stage int

const (
	StageLockWait Stage = iota
	StageWAL
	StageProjection
	StageEngine
	StageQuote
	StageCount
)

func (s Stage) String() string {
	return [...]string{"lock_wait", "wal", "projection", "engine", "quote"}[s]
}

type Config struct {
	WALPath, SnapshotPath string
	Engine                Engine
	Projector             Projector
	Sync                  bool
	Observe               func(Stage, time.Duration)
}
type Position struct {
	Yes int `json:"yes"`
	No  int `json:"no"`
}
type snapshot struct {
	Sequence  uint64                         `json:"sequence"`
	Markets   map[string]Market              `json:"markets"`
	Orders    map[string]Order               `json:"orders"`
	Balances  map[string]int64               `json:"balances"`
	Positions map[string]map[string]Position `json:"positions"`
}

type Service struct {
	mu           sync.RWMutex
	cfg          Config
	walFile      *os.File
	markets      map[string]Market
	orders       map[string]Order
	engineOrders map[uint32]string
	balances     map[string]int64
	positions    map[string]map[string]Position
	sequence     uint64
	failure      error
}

func New(wal string, engine Engine) (*Service, error) {
	return NewWithConfig(Config{WALPath: wal, Engine: engine, Sync: true})
}
func NewWithConfig(cfg Config) (*Service, error) {
	if cfg.WALPath == "" {
		return nil, errors.New("WAL path is required")
	}
	s := &Service{cfg: cfg, markets: map[string]Market{}, orders: map[string]Order{}, engineOrders: map[uint32]string{}, balances: map[string]int64{}, positions: map[string]map[string]Position{}}
	if cfg.SnapshotPath != "" {
		if err := s.loadSnapshot(); err != nil {
			return nil, err
		}
	}
	if err := s.replay(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Service) next(e Event) Event {
	s.sequence++
	e.Sequence = s.sequence
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	return e
}
func (s *Service) append(e Event) error {
	return s.appendMany([]Event{e})
}
func (s *Service) appendMany(events []Event) error {
	if s.walFile == nil {
		if err := os.MkdirAll(filepath.Dir(s.cfg.WALPath), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(s.cfg.WALPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		s.walFile = f
	}
	b, err := json.Marshal(walRecord{Version: 1, Events: events})
	if err != nil {
		return err
	}
	if len(b)+1 >= maxWALRecord {
		return errors.New("WAL batch exceeds 64 MiB record limit")
	}
	b = append(b, '\n')
	n, err := s.walFile.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if s.cfg.Sync {
		return s.walFile.Sync()
	}
	return nil
}
func (s *Service) commit(ctx context.Context, e Event) error {
	return s.commitMany(ctx, []Event{e})
}
func (s *Service) commitMany(ctx context.Context, events []Event) error {
	if s.failure != nil {
		return s.failure
	}
	for i := range events {
		events[i] = s.next(events[i])
	}
	start := time.Now()
	err := s.appendMany(events)
	s.observe(StageWAL, start)
	if err != nil {
		// Write/Sync errors can leave a durable prefix. Do not reuse sequences or
		// checkpoint an in-memory state that has fallen behind its WAL.
		return s.fail(err)
	}
	if s.cfg.Projector != nil {
		start := time.Now()
		err := s.project(ctx, events)
		s.observe(StageProjection, start)
		if err != nil {
			return s.fail(err)
		}
	}
	for _, e := range events {
		s.apply(e)
	}
	return nil
}
func (s *Service) observe(stage Stage, start time.Time) {
	if s.cfg.Observe != nil {
		s.cfg.Observe(stage, time.Since(start))
	}
}
func (s *Service) project(ctx context.Context, events []Event) error {
	if batch, ok := s.cfg.Projector.(BatchProjector); ok {
		if err := batch.ApplyBatch(ctx, events); err != nil {
			return fmt.Errorf("project event batch: %w", err)
		}
	} else {
		for _, e := range events {
			if err := s.cfg.Projector.Apply(ctx, e); err != nil {
				return fmt.Errorf("project event %d: %w", e.Sequence, err)
			}
		}
	}
	return nil
}
func (s *Service) apply(e Event) {
	if e.Sequence > s.sequence {
		s.sequence = e.Sequence
	}
	switch e.Type {
	case EventMarket:
		s.markets[e.Market.ID] = e.Market
	case EventBalance:
		s.balances[e.UserID] += e.Amount
	case EventOrder:
		if old, ok := s.orders[e.Order.ID]; ok && old.EngineID != 0 {
			delete(s.engineOrders, old.EngineID)
		}
		s.orders[e.Order.ID] = e.Order
		if e.Order.EngineID != 0 && (e.Order.Status == Pending || e.Order.Status == Open || e.Order.Status == Partial) {
			s.engineOrders[e.Order.EngineID] = e.Order.ID
		}
		s.balances[e.UserID] += e.Amount
	case EventPosition:
		if s.positions[e.UserID] == nil {
			s.positions[e.UserID] = map[string]Position{}
		}
		p := s.positions[e.UserID][e.MarketID]
		p.Yes += e.YesDelta
		p.No += e.NoDelta
		s.positions[e.UserID][e.MarketID] = p
		s.balances[e.UserID] += e.Amount
	}
}

// fail is called while holding the write lock. Recovery is deliberately a
// restart operation: the engine may also be ahead of the durable account state.
func (s *Service) fail(err error) error {
	s.failure = fmt.Errorf("%w: %v", ErrRecoveryRequired, err)
	return s.failure
}
func (s *Service) HealthError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failure
}
func (s *Service) replay() error {
	f, err := os.Open(s.cfg.WALPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var validEnd int64
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), maxWALRecord)
	// Retain the newline so valid JSON without its commit delimiter is discarded,
	// and offsets remain exact even for older CRLF records.
	scan.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i+1], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	var previous uint64
	for scan.Scan() {
		line := scan.Bytes()
		if line[len(line)-1] != '\n' {
			repair, err := os.OpenFile(s.cfg.WALPath, os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer repair.Close()
			if err = repair.Truncate(validEnd); err != nil {
				return err
			}
			return repair.Sync()
		}
		var record walRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("corrupt WAL at byte %d: %w", validEnd, err)
		}
		if record.Version == 0 {
			var e Event
			if err := json.Unmarshal(line, &e); err != nil {
				return err
			}
			if e.ID == "" {
				hash := sha256.Sum256(bytes.TrimSpace(line))
				e.ID = fmt.Sprintf("legacy-%x", hash[:16])
			}
			record.Events = []Event{e}
		} else if record.Version != 1 || len(record.Events) == 0 {
			return fmt.Errorf("invalid WAL batch at byte %d", validEnd)
		}
		pending := make([]Event, 0, len(record.Events))
		for _, e := range record.Events {
			if e.Sequence <= previous {
				return fmt.Errorf("non-increasing WAL sequence at byte %d", validEnd)
			}
			previous = e.Sequence
			if e.Sequence > s.sequence {
				pending = append(pending, e)
			}
		}
		if len(pending) > 0 {
			if s.cfg.Projector != nil {
				if batch, ok := s.cfg.Projector.(BatchProjector); ok {
					if err := batch.ApplyBatch(context.Background(), pending); err != nil {
						return err
					}
				} else {
					for _, e := range pending {
						if err := s.cfg.Projector.Apply(context.Background(), e); err != nil {
							return err
						}
					}
				}
			}
			for _, e := range pending {
				s.apply(e)
			}
		}
		validEnd += int64(len(line))
	}
	return scan.Err()
}

func (s *Service) CreateMarket(id, symbol string) error {
	return s.CreateMarketFor(id, symbol, "")
}

func (s *Service) CreateMarketFor(id, symbol, ownerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	if id == "" || symbol == "" {
		return errors.New("id and symbol are required")
	}
	if existing, ok := s.markets[id]; ok {
		if existing.Symbol == symbol && existing.OwnerID == ownerID {
			return nil
		}
		return errors.New("market id already exists with different parameters")
	}
	if s.cfg.Engine != nil {
		if err := s.cfg.Engine.AddBook(context.Background(), symbol); err != nil {
			return err
		}
	}
	return s.commit(context.Background(), Event{Type: EventMarket, Market: Market{ID: id, Symbol: symbol, OwnerID: ownerID, Status: Open}})
}
func (s *Service) Deposit(user string, cents int64) error {
	if user == "" || cents <= 0 {
		return errors.New("positive deposit and user are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	return s.commit(context.Background(), Event{Type: EventBalance, UserID: user, Amount: cents})
}
func translate(side Side, price int) (int, string, error) {
	if price < 1 || price > 99 {
		return 0, "", errors.New("price must be 1-99")
	}
	switch side {
	case BuyYes:
		return price, "buy", nil
	case SellYes:
		return price, "sell", nil
	case BuyNo:
		return 100 - price, "sell", nil
	case SellNo:
		return 100 - price, "buy", nil
	}
	return 0, "", errors.New("invalid side")
}
func isBuy(side Side) bool { return side == BuyYes || side == BuyNo }
func (s *Service) Place(ctx context.Context, o Order) error {
	start := time.Now()
	s.mu.Lock()
	s.observe(StageLockWait, start)
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	if o.ID == "" || o.UserID == "" || o.Quantity <= 0 {
		return errors.New("id, user and positive quantity are required")
	}
	if existing, ok := s.orders[o.ID]; ok {
		if existing.UserID == o.UserID && existing.MarketID == o.MarketID && existing.Side == o.Side && existing.Price == o.Price && existing.Quantity == o.Quantity {
			return nil
		}
		return errors.New("order id already exists with different parameters")
	}
	m, ok := s.markets[o.MarketID]
	if !ok || m.Status != Open {
		return errors.New("market is not open")
	}
	price, engineSide, err := translate(o.Side, o.Price)
	if err != nil {
		return err
	}
	cost := int64(o.Price * o.Quantity)
	if isBuy(o.Side) && s.balances[o.UserID] < cost {
		return errors.New("insufficient balance")
	}
	if !isBuy(o.Side) {
		pos := s.positions[o.UserID][o.MarketID]
		if o.Side == SellYes && pos.Yes < o.Quantity {
			return errors.New("insufficient YES shares")
		}
		if o.Side == SellNo && pos.No < o.Quantity {
			return errors.New("insufficient NO shares")
		}
	}
	o.EnginePrice, o.EngineSide, o.Status, o.Sequence = price, engineSide, Pending, s.sequence+1
	delta := int64(0)
	if isBuy(o.Side) {
		delta = -cost
	}
	initial := []Event{{Type: EventOrder, Order: o, UserID: o.UserID, Amount: delta}}
	if !isBuy(o.Side) {
		yesDelta, noDelta := 0, 0
		if o.Side == SellYes {
			yesDelta = -o.Quantity
		} else {
			noDelta = -o.Quantity
		}
		initial = append(initial, Event{Type: EventPosition, UserID: o.UserID, MarketID: o.MarketID, YesDelta: yesDelta, NoDelta: noDelta})
	}
	if err := s.commitMany(ctx, initial); err != nil {
		return err
	}
	if s.cfg.Engine == nil {
		o.Status = Open
		return s.commit(ctx, Event{Type: EventOrder, Order: o})
	}
	start = time.Now()
	result, err := s.cfg.Engine.AddOrder(ctx, m.Symbol, int32(price), uint32(o.Quantity), engineSide)
	s.observe(StageEngine, start)
	if err != nil {
		o.Status = Cancelled
		refund := int64(0)
		if isBuy(o.Side) {
			refund = cost
		}
		rollback := []Event{{Type: EventOrder, Order: o, UserID: o.UserID, Amount: refund}}
		if !isBuy(o.Side) {
			yesDelta, noDelta := 0, 0
			if o.Side == SellYes {
				yesDelta = o.Quantity
			} else {
				noDelta = o.Quantity
			}
			rollback = append(rollback, Event{Type: EventPosition, UserID: o.UserID, MarketID: o.MarketID, YesDelta: yesDelta, NoDelta: noDelta})
		}
		if rollbackErr := s.commitMany(ctx, rollback); rollbackErr != nil {
			return fmt.Errorf("matching engine: %w; durable rollback: %v", err, rollbackErr)
		}
		return err
	}
	o.EngineID = result.OrderID
	o.Status = Open
	events := []Event{{Type: EventOrder, Order: o}}
	events = append(events, s.buildTradeEvents(o.MarketID, result.Trades, map[uint32]string{o.EngineID: o.ID}, map[string]Order{o.ID: o})...)
	return s.commitMany(ctx, events)
}
func (s *Service) applyTrades(ctx context.Context, marketID string, trades []EngineTrade) error {
	events := s.buildTradeEvents(marketID, trades, nil, nil)
	if len(events) == 0 {
		return nil
	}
	return s.commitMany(ctx, events)
}

func (s *Service) buildTradeEvents(marketID string, trades []EngineTrade, engineOverrides map[uint32]string, staged map[string]Order) []Event {
	var events []Event
	if staged == nil {
		staged = make(map[string]Order)
	}
	lookup := func(engineID uint32) (string, bool) {
		if id, ok := engineOverrides[engineID]; ok {
			return id, true
		}
		id, ok := s.engineOrders[engineID]
		return id, ok
	}
	for _, fill := range trades {
		buyID, buyOK := lookup(fill.BuyOrderID)
		sellID, sellOK := lookup(fill.SellOrderID)
		if !buyOK || !sellOK {
			continue
		}
		events = append(events, s.fillEvents(staged, buyID, int(fill.Quantity), int(fill.Price))...)
		events = append(events, s.fillEvents(staged, sellID, int(fill.Quantity), int(fill.Price))...)
		events = append(events, Event{Type: EventTrade, Trade: Trade{MarketID: marketID, BuyOrderID: buyID, SellOrderID: sellID, Price: fill.Price, Quantity: fill.Quantity}})
	}
	return events
}
func (s *Service) fillEvents(staged map[string]Order, id string, qty, enginePrice int) []Event {
	o, ok := staged[id]
	if !ok {
		o = s.orders[id]
	}
	o.FilledQuantity += qty
	if o.FilledQuantity >= o.Quantity {
		o.Status = Filled
	} else {
		o.Status = Partial
	}
	staged[id] = o
	yes, no := 0, 0
	credit := int64(0)
	switch o.Side {
	case BuyYes:
		yes = qty
		credit = int64((o.Price - enginePrice) * qty)
	case BuyNo:
		no = qty
		credit = int64((o.Price - (100 - enginePrice)) * qty)
	case SellYes:
		credit = int64(enginePrice * qty)
	case SellNo:
		credit = int64((100 - enginePrice) * qty)
	}
	return []Event{{Type: EventOrder, Order: o}, {Type: EventPosition, UserID: o.UserID, MarketID: o.MarketID, YesDelta: yes, NoDelta: no, Amount: credit}}
}
func (s *Service) Cancel(ctx context.Context, id, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	o, ok := s.orders[id]
	if !ok || o.UserID != user {
		return errors.New("order not found")
	}
	if o.Status != Open && o.Status != Partial {
		return errors.New("order is not cancellable")
	}
	m := s.markets[o.MarketID]
	if s.cfg.Engine != nil {
		ok, err := s.cfg.Engine.RemoveOrder(ctx, m.Symbol, o.EngineID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("engine rejected cancellation")
		}
	}
	o.Status = Cancelled
	refund := int64(0)
	if isBuy(o.Side) {
		refund = int64((o.Quantity - o.FilledQuantity) * o.Price)
	}
	events := []Event{{Type: EventOrder, Order: o, UserID: o.UserID, Amount: refund}}
	if !isBuy(o.Side) {
		remaining := o.Quantity - o.FilledQuantity
		yesDelta, noDelta := 0, 0
		if o.Side == SellYes {
			yesDelta = remaining
		} else {
			noDelta = remaining
		}
		events = append(events, Event{Type: EventPosition, UserID: o.UserID, MarketID: o.MarketID, YesDelta: yesDelta, NoDelta: noDelta})
	}
	return s.commitMany(ctx, events)
}
func (s *Service) Resolve(ctx context.Context, id string, yes bool) error {
	return s.ResolveFor(ctx, id, yes, "")
}

func (s *Service) ResolveFor(ctx context.Context, id string, yes bool, ownerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	m, ok := s.markets[id]
	if !ok || m.Status != Open {
		return errors.New("market is not open")
	}
	if m.OwnerID != "" && m.OwnerID != ownerID {
		return ErrNotMarketOwner
	}
	orderIDs := make([]string, 0)
	for orderID, o := range s.orders {
		if o.MarketID == id && (o.Status == Open || o.Status == Partial) {
			orderIDs = append(orderIDs, orderID)
		}
	}
	sort.Strings(orderIDs)
	events := make([]Event, 0, len(orderIDs)*2+len(s.positions)+1)
	for _, orderID := range orderIDs {
		o := s.orders[orderID]
		if s.cfg.Engine != nil {
			removed, err := s.cfg.Engine.RemoveOrder(ctx, m.Symbol, o.EngineID)
			if err != nil {
				return fmt.Errorf("cancel order %s before resolution: %w", orderID, err)
			}
			if !removed {
				return fmt.Errorf("engine rejected cancellation for order %s", orderID)
			}
		}
		o.Status = Cancelled
		remaining := o.Quantity - o.FilledQuantity
		refund := int64(0)
		if isBuy(o.Side) {
			refund = int64(remaining * o.Price)
		}
		events = append(events, Event{Type: EventOrder, Order: o, UserID: o.UserID, Amount: refund})
		if !isBuy(o.Side) {
			yesDelta, noDelta := 0, 0
			if o.Side == SellYes {
				yesDelta = remaining
			} else {
				noDelta = remaining
			}
			events = append(events, Event{Type: EventPosition, UserID: o.UserID, MarketID: id, YesDelta: yesDelta, NoDelta: noDelta})
		}
	}
	users := make([]string, 0, len(s.positions))
	for user := range s.positions {
		users = append(users, user)
	}
	sort.Strings(users)
	for _, user := range users {
		markets := s.positions[user]
		p := markets[id]
		payout := p.No
		if yes {
			payout = p.Yes
		}
		if p.Yes != 0 || p.No != 0 {
			events = append(events, Event{Type: EventPosition, UserID: user, MarketID: id, YesDelta: -p.Yes, NoDelta: -p.No, Amount: int64(payout * 100)})
		}
	}
	if yes {
		m.Status = ResolvedYes
	} else {
		m.Status = ResolvedNo
	}
	events = append(events, Event{Type: EventMarket, Market: m})
	return s.commitMany(ctx, events)
}
func (s *Service) RecoverEngine(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	if s.cfg.Engine == nil {
		return nil
	}
	for _, m := range s.markets {
		if m.Status == Open {
			if err := s.cfg.Engine.AddBook(ctx, m.Symbol); err != nil {
				return err
			}
		}
	}
	orders := make([]Order, 0, len(s.orders))
	for _, o := range s.orders {
		if o.Status == Open || o.Status == Partial || o.Status == Pending {
			orders = append(orders, o)
		}
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].Sequence < orders[j].Sequence })
	s.engineOrders = map[uint32]string{}
	for _, o := range orders {
		o.EngineID = 0
		s.orders[o.ID] = o
	}
	for _, o := range orders {
		m := s.markets[o.MarketID]
		result, err := s.cfg.Engine.AddOrder(ctx, m.Symbol, int32(o.EnginePrice), uint32(o.Quantity-o.FilledQuantity), o.EngineSide)
		if err != nil {
			return err
		}
		o.EngineID = result.OrderID
		if o.FilledQuantity > 0 {
			o.Status = Partial
		} else {
			o.Status = Open
		}
		if err := s.commit(ctx, Event{Type: EventOrder, Order: o}); err != nil {
			return err
		}
		if err := s.applyTrades(ctx, o.MarketID, result.Trades); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Balance(user string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.balances[user]
}
func (s *Service) Position(user, market string) Position {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.positions[user][market]
}
func (s *Service) Order(id string) (Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}
func (s *Service) Market(id string) (Market, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.markets[id]
	return m, ok
}
func (s *Service) Markets() []Market {
	s.mu.RLock()
	defer s.mu.RUnlock()
	markets := make([]Market, 0, len(s.markets))
	for _, market := range s.markets {
		markets = append(markets, market)
	}
	sort.Slice(markets, func(i, j int) bool { return markets[i].ID < markets[j].ID })
	return markets
}
func (s *Service) Snapshot() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	if s.cfg.SnapshotPath == "" {
		return errors.New("snapshot path not configured")
	}
	state := snapshot{Sequence: s.sequence, Markets: s.markets, Orders: s.orders, Balances: s.balances, Positions: s.positions}
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.cfg.SnapshotPath), 0755); err != nil {
		return err
	}
	tmp := s.cfg.SnapshotPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, s.cfg.SnapshotPath); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(s.cfg.SnapshotPath))
	if err != nil {
		return err
	}
	err = directory.Sync()
	_ = directory.Close()
	if err != nil {
		return err
	}
	// The snapshot is now durable. Events through its sequence can be removed;
	// a crash before this point would still have left the previous WAL intact.
	if s.walFile != nil {
		if err = s.walFile.Truncate(0); err != nil {
			return err
		}
		return s.walFile.Sync()
	}
	if err = os.Truncate(s.cfg.WALPath, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func (s *Service) loadSnapshot() error {
	b, err := os.ReadFile(s.cfg.SnapshotPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state snapshot
	if err = json.Unmarshal(b, &state); err != nil {
		return err
	}
	s.sequence, s.markets, s.orders, s.balances, s.positions = state.Sequence, state.Markets, state.Orders, state.Balances, state.Positions
	for id, o := range s.orders {
		if o.EngineID != 0 && (o.Status == Pending || o.Status == Open || o.Status == Partial) {
			s.engineOrders[o.EngineID] = id
		}
	}
	return nil
}
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.walFile != nil {
		err := s.walFile.Close()
		s.walFile = nil
		return err
	}
	return nil
}
