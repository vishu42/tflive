import type { StateStore } from "oidc-client-ts";

const store = new Map<string, string>();

export class InMemoryStore implements StateStore {
  async set(key: string, value: string): Promise<void> {
    store.set(key, value);
  }

  async get(key: string): Promise<string | null> {
    return store.get(key) ?? null;
  }

  async remove(key: string): Promise<string | null> {
    const value = store.get(key) ?? null;
    store.delete(key);
    return value;
  }

  async getAllKeys(): Promise<string[]> {
    return Array.from(store.keys());
  }
}
