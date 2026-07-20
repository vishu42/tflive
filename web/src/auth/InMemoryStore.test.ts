import { describe, expect, it } from "vitest";
import { InMemoryStore } from "./InMemoryStore";

describe("InMemoryStore", () => {
  it("stores and retrieves a value", async () => {
    const store = new InMemoryStore();
    await store.set("key1", "value1");

    const result = await store.get("key1");
    expect(result).toBe("value1");
  });

  it("returns null for a missing key", async () => {
    const store = new InMemoryStore();
    const result = await store.get("missing");
    expect(result).toBeNull();
  });

  it("removes a key and returns the old value", async () => {
    const store = new InMemoryStore();
    await store.set("key1", "value1");

    const removed = await store.remove("key1");
    expect(removed).toBe("value1");
    expect(await store.get("key1")).toBeNull();
  });

  it("returns null when removing a missing key", async () => {
    const store = new InMemoryStore();
    const removed = await store.remove("missing");
    expect(removed).toBeNull();
  });

  it("getAllKeys returns all stored keys", async () => {
    const store = new InMemoryStore();
    await store.set("a", "1");
    await store.set("b", "2");

    const keys = await store.getAllKeys();
    expect(keys).toContain("a");
    expect(keys).toContain("b");
    expect(keys).toHaveLength(2);
  });

  it("stores and gets JSON-serialized User data", async () => {
    const store = new InMemoryStore();
    const userJson = JSON.stringify({ sub: "user_1", access_token: "at" });

    await store.set("user", userJson);
    const result = await store.get("user");

    expect(JSON.parse(result!)).toEqual({ sub: "user_1", access_token: "at" });
  });
});
