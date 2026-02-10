package com.example.client;

import org.springframework.web.client.RestTemplate;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.URI;

public class ApiClient {
    private RestTemplate restTemplate = new RestTemplate();

    public String getUser(String id) {
        return restTemplate.getForObject("/api/users/" + id, String.class);
    }

    public void createUser(String data) {
        restTemplate.postForEntity("/api/users", data, String.class);
    }

    public void deleteUser(String id) {
        restTemplate.delete("/api/users/" + id);
    }
}
