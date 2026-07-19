import type { ReactNode } from "react";
import { Outlet, useParams } from "react-router-dom";
import { useAuth } from "./AuthContext";
import { useStackCapabilities } from "./useStackCapabilities";
import type { Me } from "./types";
import type { StackCapabilities } from "./types";
import NotFound from "../app/NotFound";
import AccessDenied from "../app/AccessDenied";

type GlobalCapabilityKey = keyof Me["globalCapabilities"];
type StackCapabilityKey = keyof StackCapabilities;
export type CapabilityKey = GlobalCapabilityKey | StackCapabilityKey;

const GLOBAL_CAPABILITY_KEYS: readonly GlobalCapabilityKey[] = ["isPlatformAdmin", "canCreateStack"];

function isGlobalCapability(capability: CapabilityKey): capability is GlobalCapabilityKey {
  return (GLOBAL_CAPABILITY_KEYS as readonly string[]).includes(capability);
}

export interface RequireCapabilityProps {
  capability: CapabilityKey;
  stackId?: string;
  mode?: "gate" | "route";
  children?: ReactNode;
  fallback?: ReactNode;
}

type CapabilityState = "loading" | "allowed" | "denied";

function useCapabilityState(capability: CapabilityKey, stackId: string | undefined): CapabilityState {
  const { me, status } = useAuth();
  const params = useParams<{ stackId?: string }>();
  const resolvedStackId = stackId ?? params.stackId ?? "";
  const stackCapabilities = useStackCapabilities(resolvedStackId);

  if (isGlobalCapability(capability)) {
    if (status === "loading") {
      return "loading";
    }
    return me?.globalCapabilities[capability] ? "allowed" : "denied";
  }

  if (resolvedStackId === "" || stackCapabilities === undefined) {
    return "loading";
  }
  return stackCapabilities[capability] ? "allowed" : "denied";
}

export default function RequireCapability({ capability, stackId, mode = "gate", children, fallback }: RequireCapabilityProps) {
  const state = useCapabilityState(capability, stackId);

  // Loading is a distinct outcome from "denied" in both modes — never flash
  // denied/fallback content before the underlying auth/stack query resolves.
  if (state === "loading") {
    return null;
  }

  if (mode === "route") {
    if (state === "denied") {
      return capability === "canView" ? <NotFound /> : <AccessDenied />;
    }
    return <Outlet />;
  }

  if (state === "denied") {
    return <>{fallback ?? null}</>;
  }
  return <>{children}</>;
}
