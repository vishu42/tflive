import TemplateRunActions from "../runs/TemplateRunActions";
import TemplateRunHistory from "../runs/TemplateRunHistory";
import { useStackTemplateOutlet } from "./StackTemplateDetailShell";

// /stacks/:stackId/templates/:stackTemplateId/runs — the default tab: a header
// with the actions that start a run, and the runs below it as one list.
export default function TemplateRunsTab() {
  const { stackId, stackTemplate } = useStackTemplateOutlet();
  return (
    <section className="stack-template-tab" data-testid="template-runs-tab">
      <TemplateRunActions stackId={stackId} stackTemplate={stackTemplate} />
      <TemplateRunHistory stackId={stackId} stackTemplateId={stackTemplate.id} />
    </section>
  );
}
