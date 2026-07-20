import { UserManager, InMemoryWebStorage, WebStorageStateStore } from "oidc-client-ts";
import { oidcConfig } from "./oidcConfig";

let userManager: UserManager | null = null;

export function getUserManager(): UserManager {
  if (!userManager) {
    const store = new InMemoryWebStorage();
    userManager = new UserManager({
      ...oidcConfig,
      userStore: new WebStorageStateStore({ store }),
    });
  }
  return userManager;
}
