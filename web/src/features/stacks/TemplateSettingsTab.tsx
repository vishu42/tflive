import { RefreshCw } from "lucide-react";
import { Link } from "react-router-dom";
import RequireCapability from "../../auth/RequireCapability";
import TemplateDestroyPanel from "../runs/TemplateDestroyPanel";
import { useStackTemplateOutlet } from "./StackTemplateDetailShell";
import { isDestroyingStackTemplate } from "./stackWorkflow";

// /stacks/:stackId/templates/:stackTemplateId/settings — the actions that
// change what the template is rather than run it: choosing another revision,
// and destroying it. They live here, a tab away from the Plan button, so the
// irreversible one is never under the cursor of routine work.
export default function TemplateSettingsTab() {
  const { stackId, stackTemplate } = useStackTemplateOutlet();
  const destroying = isDestroyingStackTemplate(stackTemplate);

  return (
    <div className="stack-template-tab" data-testid="template-settings-tab">
      <section className="stack-template-revision-action" data-testid="stack-template-revision-action">
        <p className="muted">
          <RefreshCw size={16} />
          Choose a template revision
        </p>
        <RequireCapability capability="canOperate">
          {destroying ? (
            <>
              <button className="secondary-button" type="button" disabled data-testid="change-stack-template-revision-link">
                Change revision
              </button>
              <p className="muted" data-testid="upgrade-disabled-reason">
                Destroy in progress
              </p>
            </>
          ) : (
            <Link
              className="secondary-button"
              to={`/stacks/${stackId}/templates/${stackTemplate.id}/upgrade`}
              data-testid="change-stack-template-revision-link"
            >
              Change revision
            </Link>
          )}
        </RequireCapability>
      </section>
      <TemplateDestroyPanel stackId={stackId} stackTemplate={stackTemplate} />
    </div>
  );
}
