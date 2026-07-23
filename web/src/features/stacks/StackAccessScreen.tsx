import { useState, useEffect, useRef, useCallback } from "react";
import { useParams } from "react-router-dom";
import { Loader2, Search, Shield, Trash2, X } from "lucide-react";
import {
  useStackGrantsQuery,
  useSearchUsersQuery,
  useAssignStackRoleMutation,
  useRevokeStackRoleMutation
} from "../../api/queries";
import type { DirectoryUser, GrantView } from "../../api/types";
import { tenantID } from "../../config";

const ROLES = ["owner", "operator", "approver", "viewer"] as const;
type Role = (typeof ROLES)[number];

interface UndoEntry {
  userSub: string;
  role: string;
  displayName: string;
}

export default function StackAccessScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const grants = useStackGrantsQuery(tenantID, stackId);

  const [search, setSearch] = useState("");
  const [selectedUser, setSelectedUser] = useState<DirectoryUser | null>(null);
  const [selectedRole, setSelectedRole] = useState<Role>("viewer");
  const [searchFocused, setSearchFocused] = useState(false);
  const [undoEntry, setUndoEntry] = useState<UndoEntry | null>(null);
  const [mutationError, setMutationError] = useState("");
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);
  const debouncedSearch = useDebounce(search, 300);
  const searchResults = useSearchUsersQuery(tenantID, debouncedSearch);
  const assignMutation = useAssignStackRoleMutation(tenantID, stackId);
  const revokeMutation = useRevokeStackRoleMutation(tenantID, stackId);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const undoTimeoutRef = useRef<ReturnType<typeof setTimeout>>();

  const assignedSubs = new Set(
    (grants.data?.grants ?? []).map((g) => g.userSub)
  );

  const handleAssign = useCallback(async () => {
    if (!selectedUser) return;
    setMutationError("");
    try {
      await assignMutation.mutateAsync({
        user_sub: selectedUser.id,
        role: selectedRole
      });
      setSelectedUser(null);
      setSearch("");
    } catch (err) {
      setMutationError(
        err instanceof Error ? err.message : "Failed to assign role"
      );
    }
  }, [selectedUser, selectedRole, assignMutation]);

  const handleRevoke = useCallback(
    async (grant: GrantView) => {
      setMutationError("");
      setConfirmRevoke(null);
      try {
        await revokeMutation.mutateAsync(grant.userSub);
        setUndoEntry({
          userSub: grant.userSub,
          role: grant.role,
          displayName: grant.displayName
        });
      } catch (err) {
        setMutationError(
          err instanceof Error ? err.message : "Failed to revoke role"
        );
      }
    },
    [revokeMutation]
  );

  const handleUndo = useCallback(async () => {
    if (!undoEntry) return;
    setMutationError("");
    try {
      await assignMutation.mutateAsync({
        userSub: undoEntry.userSub,
        role: undoEntry.role
      });
    } catch {
      setMutationError("Failed to restore role");
    }
    setUndoEntry(null);
  }, [undoEntry, assignMutation]);

  useEffect(() => {
    if (!undoEntry) return;
    undoTimeoutRef.current = setTimeout(() => setUndoEntry(null), 5000);
    return () => clearTimeout(undoTimeoutRef.current);
  }, [undoEntry]);

  useEffect(() => {
    if (revokeMutation.isSuccess || assignMutation.isSuccess) {
      setMutationError("");
    }
  }, [revokeMutation.isSuccess, assignMutation.isSuccess]);

  return (
    <section className="workflow-grid">
      <section className="panel">
        <h2>
          <Shield size={16} />
          Current Grants
        </h2>
        {grants.isLoading && (
          <p className="muted">
            <Loader2 size={14} className="spin" /> Loading grants...
          </p>
        )}
        {grants.isError && (
          <div className="alert">
            Failed to load grants.
            <button
              className="secondary-button"
              onClick={() => grants.refetch()}
              style={{ marginLeft: 10 }}
            >
              Retry
            </button>
          </div>
        )}
        {grants.data && grants.data.grants.length === 0 && (
          <p className="muted">
            No users have been assigned access yet. Use the panel on the right
            to add the first grant.
          </p>
        )}
        {grants.data && grants.data.grants.length > 0 && (
          <ul className="grants-list">
            {grants.data.grants.map((grant) => (
              <li key={grant.userSub} className="grant-row">
                <div className="grant-user">
                  <span>{grant.displayName}</span>
                  {grant.email && <small>{grant.email}</small>}
                </div>
                <span className={`role-badge ${grant.role}`}>{grant.role}</span>
                <div className="grant-actions">
                  {confirmRevoke === grant.userSub ? (
                    <>
                      <span style={{ fontSize: "0.82rem" }}>
                        Remove access?
                      </span>
                      <button
                        className="danger"
                        onClick={() => handleRevoke(grant)}
                      >
                        Confirm
                      </button>
                      <button onClick={() => setConfirmRevoke(null)}>
                        Cancel
                      </button>
                    </>
                  ) : (
                    <button
                      className="danger"
                      onClick={() => setConfirmRevoke(grant.userSub)}
                      aria-label={`Revoke ${grant.displayName}'s ${grant.role} role`}
                    >
                      <Trash2 size={14} />
                    </button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="panel">
        <h2>
          <Search size={16} />
          Assign Role
        </h2>
        {mutationError && <div className="alert">{mutationError}</div>}
        <div className="form-row">
          <div className="search-wrapper">
            <input
              ref={searchInputRef}
              type="text"
              placeholder="Search users by name or email..."
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setSelectedUser(null);
              }}
              onFocus={() => setSearchFocused(true)}
              onBlur={() => setTimeout(() => setSearchFocused(false), 150)}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  setSearch("");
                  setSearchFocused(false);
                  searchInputRef.current?.blur();
                }
              }}
              aria-label="Search users"
              autoComplete="off"
            />
            {searchFocused &&
              debouncedSearch.length >= 2 &&
              searchResults.data && (
                <div className="search-dropdown">
                  {searchResults.data.users.length === 0 && (
                    <div className="search-result-item muted">
                      No users found
                    </div>
                  )}
                  {searchResults.data.users.map((user) => {
                    const assigned = assignedSubs.has(user.id);
                    const currentGrant = grants.data?.grants.find(
                      (g) => g.userSub === user.id
                    );
                    return (
                      <div
                        key={user.id}
                        className={`search-result-item${assigned ? " assigned" : ""}`}
                        onClick={() => {
                          if (!assigned) {
                            setSelectedUser(user);
                            setSearchFocused(false);
                          }
                        }}
                        role="option"
                        aria-selected={selectedUser?.id === user.id}
                      >
                        {user.firstName
                          ? `${user.firstName} ${user.lastName || ""}`.trim()
                          : user.username}
                        <small>
                          {user.email || user.username}
                          {currentGrant && ` — ${currentGrant.role}`}
                          {assigned && !currentGrant && " — assigned"}
                        </small>
                      </div>
                    );
                  })}
                </div>
              )}
          </div>

          {selectedUser && (
            <div className="selected-user-card">
              <span>
                {selectedUser.firstName
                  ? `${selectedUser.firstName} ${selectedUser.lastName || ""}`.trim()
                  : selectedUser.username}
              </span>
              <button
                onClick={() => setSelectedUser(null)}
                aria-label="Clear selected user"
              >
                <X size={14} />
              </button>
            </div>
          )}

          <div>
            <label htmlFor="role-select">Role</label>
            <select
              id="role-select"
              value={selectedRole}
              onChange={(e) => setSelectedRole(e.target.value as Role)}
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {r.charAt(0).toUpperCase() + r.slice(1)}
                </option>
              ))}
            </select>
          </div>

          <button
            className="primary-button"
            onClick={handleAssign}
            disabled={!selectedUser || assignMutation.isPending}
          >
            {assignMutation.isPending ? (
              <>
                <Loader2 size={14} className="spin" />
                {assignedSubs.has(selectedUser?.id ?? "")
                  ? "Replacing..."
                  : "Assigning..."}
              </>
            ) : assignedSubs.has(selectedUser?.id ?? "") ? (
              "Replace Role"
            ) : (
              "Assign Role"
            )}
          </button>
        </div>
      </section>

      {undoEntry && (
        <div className="undo-banner">
          <span>
            Removed {undoEntry.displayName}&apos;s {undoEntry.role} access.
          </span>
          <button onClick={handleUndo}>
            {assignMutation.isPending ? (
              <Loader2 size={14} className="spin" />
            ) : (
              "Undo"
            )}
          </button>
        </div>
      )}
    </section>
  );
}

function useDebounce<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}
