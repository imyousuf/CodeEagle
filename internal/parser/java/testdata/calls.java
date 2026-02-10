package com.example.service;

import com.example.util.StringUtils;
import com.example.model.User;

public class UserService {
    private void validate(User user) {
        // validation logic
    }

    public User processUser(User user) {
        validate(user);
        String name = StringUtils.capitalize(user.getName());
        User processed = User.create(name);
        return processed;
    }

    public void handleRequest(User user) {
        this.validate(user);
        User result = processUser(user);
    }
}
