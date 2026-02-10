//! Module-level documentation for the sample module.

use std::collections::HashMap;
use std::fmt;

/// A constant representing the maximum retries.
pub const MAX_RETRIES: u32 = 3;

/// A static configuration value.
static APP_NAME: &str = "sample";

/// A type alias for convenience.
pub type Result<T> = std::result::Result<T, AppError>;

/// Represents an error in the application.
#[derive(Debug)]
pub enum AppError {
    NotFound,
    InvalidInput,
    InternalError(String),
}

/// A trait for things that can be validated.
pub trait Validator {
    fn validate(&self) -> bool;
    fn error_message(&self) -> String;
}

/// A user in the system.
pub struct User {
    pub name: String,
    pub email: String,
    age: u32,
}

impl User {
    /// Creates a new user.
    pub fn new(name: String, email: String, age: u32) -> Self {
        User { name, email, age }
    }

    /// Returns the user's display name.
    pub fn display_name(&self) -> String {
        format!("{} <{}>", self.name, self.email)
    }

    fn is_adult(&self) -> bool {
        self.age >= 18
    }
}

impl Validator for User {
    fn validate(&self) -> bool {
        !self.name.is_empty() && !self.email.is_empty()
    }

    fn error_message(&self) -> String {
        String::from("User validation failed")
    }
}

impl fmt::Display for User {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.display_name())
    }
}

/// A helper function to create a greeting.
pub fn greet(user: &User) -> String {
    let name = user.display_name();
    format_greeting(&name)
}

/// Format a greeting message.
fn format_greeting(name: &str) -> String {
    format!("Hello, {}!", name)
}

/// Process multiple users.
pub fn process_users(users: &[User]) -> Vec<String> {
    let mut results = Vec::new();
    for user in users {
        if user.validate() {
            results.push(greet(user));
        }
    }
    results
}

mod helpers {
    /// A helper function inside a submodule.
    pub fn sanitize(input: &str) -> String {
        input.trim().to_lowercase()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_user_creation() {
        let user = User::new("Alice".to_string(), "alice@example.com".to_string(), 30);
        assert_eq!(user.name, "Alice");
    }

    #[test]
    fn test_validate() {
        let user = User::new("Bob".to_string(), "bob@example.com".to_string(), 25);
        assert!(user.validate());
    }

    #[test]
    fn test_greet() {
        let user = User::new("Charlie".to_string(), "charlie@example.com".to_string(), 20);
        let greeting = greet(&user);
        assert!(greeting.contains("Charlie"));
    }
}
