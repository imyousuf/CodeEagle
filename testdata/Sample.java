package com.example.sample;

import java.util.List;
import java.util.ArrayList;
import java.io.Serializable;

/**
 * Represents a generic repository for storing entities.
 *
 * @param <T> the type of entity stored
 */
public interface Repository<T> {

    /**
     * Find an entity by its ID.
     *
     * @param id the entity ID
     * @return the entity, or null if not found
     */
    T findById(long id);

    /**
     * Save an entity.
     *
     * @param entity the entity to save
     * @return the saved entity
     */
    T save(T entity);

    /**
     * Delete an entity by its ID.
     *
     * @param id the entity ID
     */
    void deleteById(long id);

    /**
     * Return all entities.
     *
     * @return list of all entities
     */
    List<T> findAll();
}

/**
 * Status of a user account.
 */
enum AccountStatus {
    ACTIVE,
    INACTIVE,
    SUSPENDED,
    DELETED
}

/**
 * Represents a user in the system.
 *
 * Implements Repository for self-management and Serializable for persistence.
 */
@SuppressWarnings("unchecked")
public class User implements Repository<User>, Serializable {

    private static final long serialVersionUID = 1L;

    private long id;
    private String name;
    private String email;
    private AccountStatus status;

    /**
     * Create a new User with the given name and email.
     *
     * @param name  the user's name
     * @param email the user's email address
     */
    public User(String name, String email) {
        this.name = name;
        this.email = email;
        this.status = AccountStatus.ACTIVE;
    }

    @Override
    public User findById(long id) {
        return null;
    }

    @Override
    public User save(User entity) {
        return entity;
    }

    @Override
    public void deleteById(long id) {
        // no-op
    }

    @Override
    public List<User> findAll() {
        return new ArrayList<>();
    }

    /**
     * Get the user's display name.
     *
     * @return formatted display name
     */
    public String getDisplayName() {
        return name + " <" + email + ">";
    }

    @Deprecated
    public String getLegacyId() {
        return String.valueOf(id);
    }

    /**
     * Check if the user account is active.
     *
     * @return true if active
     */
    public boolean isActive() {
        return status == AccountStatus.ACTIVE;
    }

    @Override
    public String toString() {
        return "User{name='" + name + "', email='" + email + "'}";
    }
}
