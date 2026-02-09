// Package sample provides example types for testing the Go parser.
// It contains a variety of Go constructs to exercise all extraction paths.
package sample

import (
	"fmt"
	"io"
)

// MaxRetries is the maximum number of retries.
const MaxRetries = 3

// DefaultName is the default name used when none is provided.
const DefaultName = "unnamed"

// version is an unexported package-level variable.
var version = "1.0.0"

// Verbose controls debug output.
var Verbose bool

// Greeter defines the interface for types that can greet.
type Greeter interface {
	// Greet returns a greeting message.
	Greet() string
}

// Writer defines an interface for writing data.
type Writer interface {
	Write(data []byte) (int, error)
	Flush() error
}

// User represents a user in the system.
type User struct {
	// Name is the user's display name.
	Name  string
	Email string
	Age   int
}

// Greet returns a greeting from the user.
func (u *User) Greet() string {
	return fmt.Sprintf("Hello, I'm %s", u.Name)
}

// String returns a string representation of the user.
func (u User) String() string {
	return fmt.Sprintf("%s <%s>", u.Name, u.Email)
}

// IsAdult reports whether the user is 18 or older.
func (u *User) IsAdult() bool {
	return u.Age >= 18
}

// UserID is a type alias for user identifiers.
type UserID string

// NewUser creates a new User with the given name and email.
func NewUser(name, email string) *User {
	return &User{Name: name, Email: email}
}

// formatName is an unexported helper function.
func formatName(first, last string) string {
	return fmt.Sprintf("%s %s", first, last)
}

// Ensure User implements Greeter at compile time.
var _ Greeter = (*User)(nil)

// Ensure io.Writer usage to avoid unused import.
var _ io.Writer = nil
