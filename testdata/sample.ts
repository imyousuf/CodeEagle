// Sample TypeScript file for parser testing.

import { EventEmitter } from 'events';
import axios from 'axios';
import type { Config } from './config';
import * as utils from './utils';

// Interface definition
export interface Serializable {
  serialize(): string;
  deserialize(data: string): void;
}

// Interface extending another
export interface Loggable extends Serializable {
  log(message: string): void;
}

// Type alias
export type UserRole = 'admin' | 'editor' | 'viewer';

// Generic type alias
export type Result<T, E = Error> = { ok: true; value: T } | { ok: false; error: E };

// Enum declaration
export enum Status {
  Active = 'ACTIVE',
  Inactive = 'INACTIVE',
  Pending = 'PENDING',
}

// Class with decorators, implements, and extends
export class UserService extends EventEmitter implements Serializable {
  private name: string;
  public readonly id: number;

  constructor(name: string, id: number) {
    super();
    this.name = name;
    this.id = id;
  }

  serialize(): string {
    return JSON.stringify({ name: this.name, id: this.id });
  }

  deserialize(data: string): void {
    const parsed = JSON.parse(data);
    this.name = parsed.name;
  }

  async fetchData(url: string): Promise<string> {
    const response = await axios.get(url);
    return response.data;
  }

  private formatName(): string {
    return this.name.toUpperCase();
  }
}

// Regular exported function
export function createUser(name: string, role: UserRole): UserService {
  return new UserService(name, 1);
}

// Async exported function
export async function fetchUsers(endpoint: string): Promise<UserService[]> {
  const response = await axios.get(endpoint);
  return response.data;
}

// Arrow function variable
export const formatRole = (role: UserRole): string => {
  return role.charAt(0).toUpperCase() + role.slice(1);
};

// Generic function
export function identity<T>(value: T): T {
  return value;
}

// Default export function
export default function main(): void {
  console.log('Hello from TypeScript');
}

// Unexported helper
function helperFunc(x: number): number {
  return x * 2;
}

// Arrow function without export
const internalHelper = (s: string): string => s.trim();

// React-style function component (returns JSX)
export const UserCard: React.FC<{ name: string }> = ({ name }) => {
  return <div className="user-card">{name}</div>;
};

// React function component (regular function returning JSX)
export function UserList({ users }: { users: string[] }) {
  return (
    <ul>
      {users.map((u) => (
        <li key={u}>{u}</li>
      ))}
    </ul>
  );
}

// Namespace declaration
export namespace Validators {
  export function isValid(s: string): boolean {
    return s.length > 0;
  }
}
