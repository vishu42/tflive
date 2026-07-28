import { RefreshCw } from "lucide-react";
import { useParams } from "react-router-dom";
import {
  useCreateStackCredentialMutation,
  useDeleteStackCredentialMutation,
  useStackCredentialsQuery
} from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import CredentialsPanel from "./CredentialsPanel";

export default function EnvironmentScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const credentialsQuery = useStackCredentialsQuery(tenantID, stackId);
  const createMutation = useCreateStackCredentialMutation(tenantID, stackId);
  const deleteMutation = useDeleteStackCredentialMutation(tenantID, stackId);
  const boundary = useQueryErrorBoundary(credentialsQuery.error);

  if (credentialsQuery.status === "pending") {
    return (
      <section className="stack-environment-screen" data-testid="environment-loading">
        <p className="muted">Loading environment…</p>
      </section>
    );
  }

  if (credentialsQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="stack-environment-screen" data-testid="environment-error">
        <p className="muted">Something went wrong while loading the environment.</p>
        <button className="primary-button" type="button" data-testid="environment-retry" onClick={() => credentialsQuery.refetch()}>
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  return (
    <section className="stack-environment-screen" data-testid="environment-screen">
      <CredentialsPanel
        title="Environment credentials"
        credentials={credentialsQuery.data ?? []}
        loading={credentialsQuery.isPending}
        busy={createMutation.isPending || deleteMutation.isPending}
        onCreate={async (name, value) => {
          await createMutation.mutateAsync({ name, value });
        }}
        onDelete={(id) => deleteMutation.mutateAsync(id)}
      />
    </section>
  );
}
