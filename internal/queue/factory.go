package queue

import (
	"fmt"
	"sync"
)

// DriverFactory creates Publisher and Consumer instances for a given broker URL.
type DriverFactory struct {
	NewPublisher func(url, queueName string) (Publisher, error)
	NewConsumer  func(url, queueName, group string) (Consumer, error)
}

var (
	driversMu sync.RWMutex
	drivers   = make(map[string]DriverFactory)
)

// Register makes a queue driver available by name.
// Called from init() in driver packages gated by build tags.
func Register(name string, factory DriverFactory) {
	driversMu.Lock()
	defer driversMu.Unlock()
	drivers[name] = factory
}

// NewPublisher creates a Publisher for the named driver.
func NewPublisher(driver, url, queueName string) (Publisher, error) {
	f, err := getDriver(driver)
	if err != nil {
		return nil, err
	}
	return f.NewPublisher(url, queueName)
}

// NewConsumer creates a Consumer for the named driver.
func NewConsumer(driver, url, queueName, group string) (Consumer, error) {
	f, err := getDriver(driver)
	if err != nil {
		return nil, err
	}
	return f.NewConsumer(url, queueName, group)
}

func getDriver(name string) (DriverFactory, error) {
	driversMu.RLock()
	defer driversMu.RUnlock()
	f, ok := drivers[name]
	if !ok {
		return DriverFactory{}, fmt.Errorf("queue driver %q is not compiled in; rebuild with -tags %s", name, name)
	}
	return f, nil
}

// Drivers returns the names of registered drivers (for testing/diagnostics).
func Drivers() []string {
	driversMu.RLock()
	defer driversMu.RUnlock()
	names := make([]string, 0, len(drivers))
	for name := range drivers {
		names = append(names, name)
	}
	return names
}
