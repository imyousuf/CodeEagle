using System;
using System.Collections.Generic;
using System.Linq;

namespace MyApp.Models
{
    /// <summary>
    /// Interface for repository operations.
    /// </summary>
    public interface IRepository<T>
    {
        T FindById(int id);
        IEnumerable<T> GetAll();
        void Save(T entity);
        void Delete(int id);
    }

    /// <summary>
    /// Represents a user in the system.
    /// </summary>
    public class User
    {
        public int Id { get; set; }
        public string Name { get; set; }
        public string Email { get; set; }
        public bool IsActive { get; set; }

        public User(int id, string name, string email)
        {
            Id = id;
            Name = name;
            Email = email;
            IsActive = true;
        }

        /// <summary>
        /// Gets the display name.
        /// </summary>
        public string GetDisplayName()
        {
            return $"{Name} ({Email})";
        }

        public bool Validate()
        {
            return !string.IsNullOrEmpty(Name) && !string.IsNullOrEmpty(Email);
        }
    }

    /// <summary>
    /// Account status values.
    /// </summary>
    public enum AccountStatus
    {
        Active,
        Inactive,
        Suspended,
        Deleted
    }

    /// <summary>
    /// A point in 2D space.
    /// </summary>
    public struct Point
    {
        public double X { get; set; }
        public double Y { get; set; }

        public double DistanceTo(Point other)
        {
            return Math.Sqrt(Math.Pow(X - other.X, 2) + Math.Pow(Y - other.Y, 2));
        }
    }

    /// <summary>
    /// User repository implementation.
    /// </summary>
    public class UserRepository : IRepository<User>
    {
        private readonly List<User> _users = new List<User>();
        public const int MaxUsers = 1000;

        public User FindById(int id)
        {
            return _users.FirstOrDefault(u => u.Id == id);
        }

        public IEnumerable<User> GetAll()
        {
            return _users.AsReadOnly();
        }

        public void Save(User entity)
        {
            if (!entity.Validate())
            {
                throw new ArgumentException("Invalid user");
            }
            _users.Add(entity);
        }

        public void Delete(int id)
        {
            var user = FindById(id);
            if (user != null)
            {
                _users.Remove(user);
            }
        }
    }
}
