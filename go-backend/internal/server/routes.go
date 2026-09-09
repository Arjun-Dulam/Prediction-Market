package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"go-backend/internal/auth"
	"go-backend/internal/cache"
	"go-backend/internal/trading"
	"go-backend/internal/ws"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	enginepb "go-backend/internal/engine/pb"
)

func (s *Server) RegisterRoutes() http.Handler {
	return s.registerRoutes(true)
}

func (s *Server) registerRoutes(logRequests bool) http.Handler {
	r := chi.NewRouter()
	if s.metrics != nil {
		r.Use(s.metrics.Middleware)
	}
	if logRequests {
		r.Use(middleware.Logger)
	}
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   strings.Split(envOr("CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173"), ","),
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/", s.HelloWorldHandler)

	r.Get("/health", s.healthHandler)
	if s.metrics != nil {
		r.Get("/metrics", s.metrics.ServeHTTP)
	}
	if s.trading == nil {
		r.Post("/api/v1/orders", s.placeOrderHandler)
	}
	if s.auth != nil {
		r.Post("/api/v1/users/register", s.registerHandler)
		r.Post("/api/v1/users/login", s.loginHandler)
	}
	secured := func(next http.Handler) http.Handler {
		if s.auth == nil {
			return next
		}
		return s.auth.Middleware(next)
	}
	r.Get("/api/v1/markets/{symbol}/quote", s.quoteHandler)
	r.Get("/ws", ws.ServeHTTP(s.hub))
	r.With(secured).Post("/api/v1/markets", s.createMarketHandler)
	r.Get("/api/v1/markets", s.marketsHandler)
	r.With(secured).Post("/api/v1/trading/orders", s.durableOrderHandler)
	r.With(secured).Get("/api/v1/trading/orders/{id}", s.orderHandler)
	r.With(secured).Delete("/api/v1/trading/orders/{id}", s.cancelOrderHandler)
	r.With(secured).Post("/api/v1/balances/deposit", s.depositHandler)
	r.With(secured).Get("/api/v1/balances/{userID}", s.balanceHandler)
	r.With(secured).Get("/api/v1/positions/{userID}/{marketID}", s.positionHandler)
	r.Get("/api/v1/markets/{id}", s.marketHandler)
	r.With(secured).Post("/api/v1/markets/{id}/resolve", s.resolveMarketHandler)

	return r
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) registerHandler(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil {
		http.Error(w, "invalid registration", http.StatusBadRequest)
		return
	}
	id, token, err := s.auth.Register(r.Context(), req.Email, req.Username, req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"user_id": id, "token": token})
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil {
		http.Error(w, "invalid login", http.StatusBadRequest)
		return
	}
	id, token, err := s.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"user_id": id, "token": token})
}

type marketRequest struct {
	ID     string `json:"id"`
	Symbol string `json:"symbol"`
}

func (s *Server) createMarketHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", 503)
		return
	}
	var req marketRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.ID == "" || strings.TrimSpace(req.Symbol) == "" {
		http.Error(w, "id and symbol are required", 400)
		return
	}
	ownerID, _ := auth.UserID(r.Context())
	if err := s.trading.CreateMarketFor(req.ID, req.Symbol, ownerID); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(req)
}

type durableOrderRequest struct {
	ID       string       `json:"id"`
	UserID   string       `json:"user_id"`
	MarketID string       `json:"market_id"`
	Side     trading.Side `json:"side"`
	Price    int          `json:"price"`
	Quantity int          `json:"quantity"`
}

func (s *Server) durableOrderHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", 503)
		return
	}
	var req durableOrderRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "invalid order", 400)
		return
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	userID, err := auth.RequireOwner(r.Context(), req.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	req.UserID = userID
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.trading.Place(ctx, trading.Order{ID: req.ID, UserID: req.UserID, MarketID: req.MarketID, Side: req.Side, Price: req.Price, Quantity: req.Quantity}); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, trading.ErrRecoveryRequired) || errors.Is(err, trading.ErrOrderQueueFull) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), status)
		return
	}
	if s.metrics != nil {
		s.metrics.OrderAccepted()
	}
	s.hub.Publish(ws.Message{Channel: "market:" + req.MarketID, Type: "order", Data: map[string]string{"order_id": req.ID}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"order_id": req.ID})
}

func (s *Server) depositHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		UserID string `json:"user_id"`
		Cents  int64  `json:"cents"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "invalid deposit", http.StatusBadRequest)
		return
	}
	userID, err := auth.RequireOwner(r.Context(), req.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	req.UserID = userID
	if err := s.trading.Deposit(req.UserID, req.Cents); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) balanceHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", http.StatusServiceUnavailable)
		return
	}
	userID, err := auth.RequireOwner(r.Context(), chi.URLParam(r, "userID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int64{"cents": s.trading.Balance(userID)})
}

func (s *Server) positionHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", http.StatusServiceUnavailable)
		return
	}
	userID, err := auth.RequireOwner(r.Context(), chi.URLParam(r, "userID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.trading.Position(userID, chi.URLParam(r, "marketID")))
}

func (s *Server) orderHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", http.StatusServiceUnavailable)
		return
	}
	order, ok := s.trading.Order(chi.URLParam(r, "id"))
	if !ok {
		http.Error(w, "order not found", http.StatusNotFound)
		return
	}
	if _, err := auth.RequireOwner(r.Context(), order.UserID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(order)
}

func (s *Server) marketHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", http.StatusServiceUnavailable)
		return
	}
	market, ok := s.trading.Market(chi.URLParam(r, "id"))
	if !ok {
		http.Error(w, "market not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(market)
}

func (s *Server) marketsHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.trading.Markets())
}

func (s *Server) cancelOrderHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", http.StatusServiceUnavailable)
		return
	}
	userID, err := auth.RequireOwner(r.Context(), r.URL.Query().Get("user_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if err := s.trading.Cancel(r.Context(), chi.URLParam(r, "id"), userID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// The trading worker publishes once per affected market/group after committing.
// Out-of-order publishers are rejected atomically by Redis. WebSocket clients
// also compare sequence numbers because concurrent sends can still interleave.
func (s *Server) publishQuotes(quotes []trading.MarketQuote) {
	start := time.Now()
	if s.metrics != nil {
		defer func() { s.metrics.Observe(trading.StageQuote, time.Since(start)) }()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, q := range quotes {
		quote := cache.Quote{Bid: q.Bid, Ask: q.Ask, Sequence: q.Sequence, UpdatedAt: time.Now().UTC()}
		if s.quotes != nil {
			accepted, err := s.quotes.SetVersioned(ctx, q.Symbol, quote)
			if err != nil || !accepted {
				continue
			}
		}
		s.hub.Publish(ws.Message{Channel: "market:" + q.MarketID, Type: "quote", Data: quote})
	}
}
func (s *Server) resolveMarketHandler(w http.ResponseWriter, r *http.Request) {
	if s.trading == nil {
		http.Error(w, "trading unavailable", 503)
		return
	}
	var req struct {
		Outcome string `json:"outcome"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || (req.Outcome != "yes" && req.Outcome != "no") {
		http.Error(w, "outcome must be yes or no", 400)
		return
	}
	ownerID, _ := auth.UserID(r.Context())
	if err := s.trading.ResolveFor(r.Context(), chi.URLParam(r, "id"), req.Outcome == "yes", ownerID); err != nil {
		if errors.Is(err, trading.ErrNotMarketOwner) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), 404)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type placeOrderRequest struct {
	Symbol   string `json:"symbol"`
	Price    int32  `json:"price"`
	Quantity uint32 `json:"quantity"`
	Side     string `json:"side"`
}

type orderResponse struct {
	OrderID uint32 `json:"order_id"`
}

func (s *Server) placeOrderHandler(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		http.Error(w, "matching engine is unavailable", http.StatusServiceUnavailable)
		return
	}

	var request placeOrderRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid order payload", http.StatusBadRequest)
		return
	}

	request.Symbol = strings.TrimSpace(request.Symbol)
	if request.Symbol == "" || request.Price < 1 || request.Price > 99 || request.Quantity == 0 {
		http.Error(w, "symbol, price (1-99), and positive quantity are required", http.StatusBadRequest)
		return
	}

	var side enginepb.Side
	switch strings.ToLower(request.Side) {
	case "buy":
		side = enginepb.Side_SIDE_BUY
	case "sell":
		side = enginepb.Side_SIDE_SELL
	default:
		http.Error(w, "side must be buy or sell", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	orderID, err := s.engine.AddOrder(ctx, request.Symbol, request.Price, request.Quantity, side)
	if err != nil {
		http.Error(w, "matching engine rejected order", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(orderResponse{OrderID: orderID})
}

func (s *Server) quoteHandler(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		http.Error(w, "matching engine is unavailable", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	symbol := chi.URLParam(r, "symbol")
	if s.quotes != nil {
		if quote, err := s.quotes.Get(ctx, symbol); err == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(quote)
			return
		}
	}
	if s.trading != nil {
		q, err := s.trading.Quote(ctx, symbol)
		if err != nil {
			http.Error(w, "quote snapshot unavailable", http.StatusServiceUnavailable)
			return
		}
		quote := cache.Quote{Bid: q.Bid, Ask: q.Ask, Sequence: q.Sequence, UpdatedAt: time.Now().UTC()}
		if s.quotes != nil {
			_, _ = s.quotes.SetVersioned(ctx, symbol, quote)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(quote)
		return
	}
	bid, err := s.engine.BestBid(ctx, symbol)
	if err != nil {
		http.Error(w, "matching engine unavailable", http.StatusBadGateway)
		return
	}
	ask, err := s.engine.BestAsk(ctx, symbol)
	if err != nil {
		http.Error(w, "matching engine unavailable", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	quote := cache.Quote{Bid: bid, Ask: ask}
	if s.quotes != nil {
		_ = s.quotes.Set(ctx, symbol, quote)
	}
	_ = json.NewEncoder(w).Encode(quote)
}

func (s *Server) HelloWorldHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "Prediction Market Exchange"})
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	databaseStatus := s.db.Health()
	response := map[string]any{"status": "up", "database": databaseStatus}
	healthy := databaseStatus["status"] == "up"
	if s.trading != nil {
		if err := s.trading.HealthError(); err != nil {
			response["durability"] = map[string]string{"status": "down", "error": err.Error()}
			healthy = false
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if s.quotes != nil {
		if err := s.quotes.Ping(ctx); err != nil {
			response["redis"] = map[string]string{"status": "down", "error": err.Error()}
			healthy = false
		} else {
			response["redis"] = map[string]string{"status": "up"}
		}
	}
	if checker, ok := s.engine.(interface{ Ready(context.Context) error }); ok {
		if err := checker.Ready(ctx); err != nil {
			response["engine"] = map[string]string{"status": "down", "error": err.Error()}
			healthy = false
		} else {
			response["engine"] = map[string]string{"status": "up"}
		}
	}
	if !healthy {
		response["status"] = "down"
	}
	w.Header().Set("Content-Type", "application/json")
	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	jsonResp, _ := json.Marshal(response)
	_, _ = w.Write(jsonResp)
}
