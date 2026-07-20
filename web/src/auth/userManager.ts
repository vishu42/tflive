import { UserManager, WebStorageStateStore } from "oidc-client-ts";
import { InMemoryStore } from "./InMemoryStore";
import { oidcConfig } from "./oidcConfig";

let userManager: UserManager | null = null;

export function getUserManager(): UserManager {
  if (!userManager) {
    userManager = new UserManager({
      ...oidcConfig,
      userStore: new WebStorageStateStore({ store: new InMemoryStore() }),
    });
  }
  return userManager;
}
