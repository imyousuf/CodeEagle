// Sample JavaScript file for parser testing.

// ESM imports
import { readFile, writeFile } from 'fs/promises';
import path from 'path';
import * as crypto from 'crypto';

// CommonJS require
const express = require('express');
const { Router } = require('express');
const lodash = require('lodash');

// Class declaration
export class HttpClient {
  constructor(baseURL) {
    this.baseURL = baseURL;
    this.headers = {};
  }

  async get(endpoint) {
    const url = `${this.baseURL}${endpoint}`;
    const response = await fetch(url, { headers: this.headers });
    return response.json();
  }

  async post(endpoint, data) {
    const url = `${this.baseURL}${endpoint}`;
    const response = await fetch(url, {
      method: 'POST',
      headers: { ...this.headers, 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    });
    return response.json();
  }

  setHeader(key, value) {
    this.headers[key] = value;
  }
}

// Regular exported function
export function createClient(baseURL) {
  return new HttpClient(baseURL);
}

// Async function
export async function fetchData(url) {
  const response = await fetch(url);
  return response.json();
}

// Arrow function export
export const formatURL = (base, path) => {
  return `${base}/${path}`;
};

// Default export function
export default function main() {
  console.log('Hello from JavaScript');
}

// Unexported helper
function helperFunc(x) {
  return x * 2;
}

// Unexported arrow function
const internalHelper = (s) => s.trim();

// React-style component (JSX)
export function UserCard({ name, email }) {
  return (
    <div className="user-card">
      <h2>{name}</h2>
      <p>{email}</p>
    </div>
  );
}

// Arrow component
export const Badge = ({ label }) => {
  return <span className="badge">{label}</span>;
};

// CommonJS module.exports pattern
// module.exports = { HttpClient, createClient, fetchData };

// Named exports at bottom
// export { HttpClient, createClient };
