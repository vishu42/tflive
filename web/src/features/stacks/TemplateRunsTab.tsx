import TemplateRunActions from "../runs/TemplateRunActions";
import TemplateRunHistory from "../runs/TemplateRunHistory";
import { useStackTemplateOutlet } from "./StackTemplateDetailShell";

// /stacks/:stackId/templates/:stackTemplateId/runs — the default tab. The
// controls that start a run sit above the history they add to.
export default function TemplateRunsTab() {
  const { stackId, stackTemplate } = useStackTemplateOutlet();
  return (
    <div className="stack-template-tab" data-testid="template-runs-tab">
      <TemplateRunActions stackId={stackId} stackTemplate={stackTemplate} />
      <TemplateRunHistory stackId={stackId} stackTemplateId={stackTemplate.id} />
    </div>
  );
}
