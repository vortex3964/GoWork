package testdata

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// ==============================================================================
// User Models and Methods
// ==============================================================================

type User struct {
	ID        int
	Name      string
	Email     string
	Role      string
	IsActive  bool
	CreatedAt time.Time
}

func NewUser(id int, name, email, role string) *User {
	return &User{
<<<<<<< old
		ID:        id,
		Name:      name,
		Email:     email,
		Role:      role,
=======
		ID:        id,
		Name:      name,
		Email:     email,
		Role:      "viewer",
>>>>>>> Ai change
		IsActive:  true,
		CreatedAt: time.Now(),
	}
}

func (u *User) IsAdmin() bool {
	return u.Role == "admin" || u.Role == "superadmin"
}

func (u *User) Deactivate() {
	u.IsActive = false
}

func (u *User) ValidateEmail() error {
<<<<<<< old
	if !strings.Contains(u.Email, "@") {
		return errors.New("invalid email")
	}
=======
	if !strings.Contains(u.Email, "@") || !strings.Contains(u.Email, ".") {
		return errors.New("invalid email format: missing domain")
	}
>>>>>>> Ai change
	return nil
}

func (u *User) String() string {
<<<<<<< old
	return fmt.Sprintf("User %s (ID: %d)", u.Name, u.ID)
=======
	return fmt.Sprintf("User: %s | ID: %d | Role: %s", u.Name, u.ID, u.Role)
>>>>>>> Ai change
}

// ==============================================================================
// Product Models and Methods
// ==============================================================================

type Product struct {
	ID          int
	Name        string
	Description string
	Price       float64
	Category    string
	StockCount  int
}

func NewProduct(id int, name, desc string, price float64, category string, stock int) *Product {
	return &Product{
		ID:          id,
		Name:        name,
		Description: desc,
		Price:       price,
		Category:    category,
		StockCount:  stock,
	}
}

func (p *Product) CalculateDiscount() float64 {
<<<<<<< old
	// Apply a flat 10% discount to all products
	return p.Price * 0.10
=======
	// Apply tiered discounts based on category
	if p.Category == "Electronics" {
		return p.Price * 0.15
	}
	return p.Price * 0.05
>>>>>>> Ai change
}

func (p *Product) IsAvailable(qty int) bool {
	return p.StockCount >= qty
}

// ==============================================================================
// Order and Shopping Cart Models
// ==============================================================================

type OrderItem struct {
	ProductID int
	Quantity  int
	UnitPrice float64
}

type Order struct {
	ID          string
	UserID      int
	Items       []OrderItem
	TotalAmount float64
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func NewOrder(id string, userID int) *Order {
	return &Order{
		ID:        id,
		UserID:    userID,
		Items:     make([]OrderItem, 0),
		Status:    "Created",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func (o *Order) AddItem(item OrderItem) {
	o.Items = append(o.Items, item)
	o.TotalAmount += float64(item.Quantity) * item.UnitPrice
}

func (o *Order) Checkout() error {
	if len(o.Items) == 0 {
		return errors.New("cannot checkout empty order")
	}
<<<<<<< old
	o.Status = "Shipped"
	o.UpdatedAt = time.Now()
=======
	o.Status = "Pending_Payment"
	o.UpdatedAt = time.Now().UTC()
>>>>>>> Ai change
	return nil
}

// ==============================================================================
// Mock Database Service
// ==============================================================================

type DB struct {
	mu    sync.RWMutex
	users map[int]*User
}

func NewDB() *DB {
	return &DB{
		users: make(map[int]*User),
	}
}

func (db *DB) Connect() (bool, error) {
<<<<<<< old
	fmt.Println("Connecting to the database...")
	// mock connection delay
	return true, nil
=======
	log.Println("Attempting to establish database connection...")
	// mock connection delay with context timeout simulation
	time.Sleep(100 * time.Millisecond)
	return true, nil
>>>>>>> Ai change
}

func (db *DB) SaveUser(u *User) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	
	if u.ID <= 0 {
		return errors.New("invalid user ID")
	}
	db.users[u.ID] = u
	return nil
}

func (db *DB) GetUser(id int) (*User, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	user, exists := db.users[id]
	if !exists {
		return nil, errors.New("user not found")
	}
	return user, nil
}

func (db *DB) DeleteUser(id int) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.users[id]; !exists {
		return errors.New("user not found")
	}
	delete(db.users, id)
	return nil
}

// ==============================================================================
// Inventory Service
// ==============================================================================

type InventoryService struct {
	mu       sync.Mutex
	products map[int]*Product
}

func NewInventoryService() *InventoryService {
	return &InventoryService{
		products: make(map[int]*Product),
	}
}

func (inv *InventoryService) AddProduct(p *Product) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.products[p.ID] = p
}

func (inv *InventoryService) UpdateStock(productID int, diff int) error {
	inv.mu.Lock()
	defer inv.mu.Unlock()

	p, exists := inv.products[productID]
	if !exists {
		return errors.New("product does not exist in inventory")
	}

	if p.StockCount+diff < 0 {
		return errors.New("insufficient stock")
	}

	p.StockCount += diff
	return nil
}

// ==============================================================================
// Main Application / Runner
// ==============================================================================

type AppConfig struct {
	Environment string
	Port        int
	DebugMode   bool
}

type Application struct {
	Config AppConfig
	DB     *DB
	Inv    *InventoryService
}

func NewApplication(cfg AppConfig) *Application {
	return &Application{
		Config: cfg,
		DB:     NewDB(),
		Inv:    NewInventoryService(),
	}
}

func (app *Application) Start(ctx context.Context) error {
	_, err := app.DB.Connect()
	if err != nil {
		return fmt.Errorf("failed to initialize db: %w", err)
	}

<<<<<<< old
	fmt.Printf("Starting application on port %d\n", app.Config.Port)
	// Server startup logic mock
	return nil
=======
	log.Printf("Starting %s application server on :%d...", app.Config.Environment, app.Config.Port)
	if app.Config.DebugMode {
		log.Println("Debug mode is ENABLED")
	}
	// Advanced server startup logic mock
	return nil
>>>>>>> Ai change
}

func (app *Application) ProcessQueue() {
	// Mock queue processing
	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)
	}
}

func (app *Application) HandleError(err error) {
	if err == nil {
		return
	}
	
<<<<<<< old
	// Simple panic on any error
	log.Fatalf("Critical error: %v", err)
=======
	// Graceful error handling
	if os.Getenv("ENV") == "production" {
		log.Printf("Recoverable error encountered: %v. Continuing...", err)
	} else {
		log.Panicf("Development panic: %v", err)
	}
>>>>>>> Ai change
}

// Dummy main function for file completeness
func main() {
	cfg := AppConfig{
		Environment: "development",
		Port:        8080,
		DebugMode:   true,
	}

	app := NewApplication(cfg)
	ctx := context.Background()

	if err := app.Start(ctx); err != nil {
		app.HandleError(err)
	}
}
