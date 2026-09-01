import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import HeroGraphic from "../shared/HeroGraphic";
import StatBand from "../shared/StatBand";
import StatusRow from "../shared/StatusRow";
import "./styleguide.css";

/**
 * Development-only gallery of the design system.
 *
 * It renders the real components rather than copies of their markup, so it
 * cannot drift from them, and it reads token values out of the cascade at
 * runtime rather than restating them, so the swatches cannot drift from
 * tokens.css either.
 *
 * It is mounted OUTSIDE SessionProvider (see app/router.tsx) so it renders
 * with no Keycloak and no backend — the app itself cannot mount without
 * them, which makes this the only way to see the design system locally.
 */

const SECTIONS: { id: string; title: string }[] = [
  { id: "colour", title: "Colour" },
  { id: "type", title: "Typography" },
  { id: "radii", title: "Radii" },
  { id: "shadows", title: "Shadows" },
  { id: "buttons", title: "Buttons" },
  { id: "inputs", title: "Inputs" },
  { id: "panels", title: "Panels" },
  { id: "status", title: "Status tones" },
  { id: "roles", title: "Role badges" },
  { id: "stats", title: "Stat band" },
  { id: "messaging", title: "Messaging" },
  { id: "tabs", title: "Tabs" },
  { id: "log", title: "Log panel" },
  { id: "showpiece", title: "Showpieces" }
];

const COLOUR_TOKENS = [
  "--color-bg",
  "--color-fg",
  "--color-card",
  "--color-muted",
  "--color-muted-fg",
  "--color-border",
  "--color-accent",
  "--color-accent-2",
  "--color-success",
  "--color-warning",
  "--color-danger",
  "--color-success-dot",
  "--color-warning-dot"
];

const RADIUS_TOKENS = ["--radius-sm", "--radius-md", "--radius-lg", "--radius-xl", "--radius-full"];

const SHADOW_TOKENS = [
  "--shadow-sm",
  "--shadow-md",
  "--shadow-lg",
  "--shadow-xl",
  "--shadow-accent",
  "--shadow-accent-lg"
];

const TYPE_STEPS = [
  "--text-xs",
  "--text-sm",
  "--text-base",
  "--text-lg",
  "--text-xl",
  "--text-2xl",
  "--text-3xl",
  "--text-4xl",
  "--text-5xl"
];

/** Reads custom properties off the document root, so nothing is restated here. */
function useTokenValues(names: string[]): Record<string, string> {
  const [values, setValues] = useState<Record<string, string>>({});

  useEffect(() => {
    const computed = getComputedStyle(document.documentElement);
    const next: Record<string, string> = {};
    for (const name of names) {
      next[name] = computed.getPropertyValue(name).trim();
    }
    setValues(next);
    // names is a module-level constant array per call site; re-reading on every
    // render would be wasteful and the tokens cannot change at runtime.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return values;
}

function Section({ id, title, note, children }: { id: string; title: string; note?: string; children: ReactNode }) {
  return (
    <section className="sg__section" id={id}>
      <h2>{title}</h2>
      {note && <p className="sg__note">{note}</p>}
      {children}
    </section>
  );
}

function Specimen({
  label,
  hint,
  stack = false,
  children
}: {
  label: string;
  hint?: string;
  stack?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="sg__specimen">
      <div className="sg__specimen-label">
        <span>{label}</span>
        {hint && <span>{hint}</span>}
      </div>
      <div className={`sg__specimen-body${stack ? " sg__specimen-body--stack" : ""}`}>{children}</div>
    </div>
  );
}

export default function StyleGuide() {
  const colours = useTokenValues(COLOUR_TOKENS);
  const radii = useTokenValues(RADIUS_TOKENS);
  const shadows = useTokenValues(SHADOW_TOKENS);
  const type = useTokenValues(TYPE_STEPS);

  return (
    <div className="sg" data-testid="styleguide">
      <aside className="sg__nav">
        <div className="sg__brand">
          tflive
          <span>Design system</span>
        </div>
        <nav aria-label="Design system sections">
          <ol>
            {SECTIONS.map((section) => (
              <li key={section.id}>
                <a href={`#${section.id}`}>{section.title}</a>
              </li>
            ))}
          </ol>
        </nav>
      </aside>

      <main className="sg__main">
        <div className="sg__intro">
          <h1>Design system</h1>
          <p>
            Every example below renders the real component and reads its token values out of the
            cascade, so this page cannot drift from the implementation. It is registered only in
            development builds, and it mounts outside the auth provider so it works with no backend.
          </p>
        </div>

        <Section
          id="colour"
          title="Colour"
          note="The accent is the only saturated colour in ordinary use. The three status colours are reserved for state. Note that --color-success-dot and --color-warning-dot are brighter variants restricted to dots and fills: they measure roughly 3.2:1 on white and fail AA for text, which is why the text-bearing tokens are darker."
        >
          <Specimen label="Palette" hint="values read from tokens.css at runtime">
            <div className="sg__swatches">
              {COLOUR_TOKENS.map((token) => (
                <dl className="sg__swatch" key={token}>
                  <div
                    className="sg__swatch-chip"
                    style={{ background: `var(${token})` }}
                    aria-hidden="true"
                  />
                  <dt>{token}</dt>
                  <dd>{colours[token] || "—"}</dd>
                </dl>
              ))}
            </div>
          </Specimen>

          <Specimen label="Gradient" hint="--gradient-accent">
            <div
              style={{
                width: "100%",
                height: "72px",
                borderRadius: "var(--radius-lg)",
                background: "var(--gradient-accent)"
              }}
            />
          </Specimen>
        </Section>

        <Section
          id="type"
          title="Typography"
          note="Archivo carries display headings, Inter carries body and UI, JetBrains Mono carries every technical signal — labels, IDs, timestamps, status and logs."
        >
          <Specimen label="Families" stack>
            <p style={{ fontFamily: "var(--font-display)", fontWeight: 600, fontSize: "var(--text-4xl)", margin: 0 }}>
              Archivo display
            </p>
            <p style={{ fontFamily: "var(--font-body)", fontSize: "var(--text-lg)", margin: "var(--space-4) 0 0" }}>
              Inter body — the quick brown fox jumps over the lazy dog, 0123456789
            </p>
            <p style={{ fontFamily: "var(--font-mono)", fontSize: "var(--text-sm)", margin: "var(--space-4) 0 0" }}>
              JetBrains Mono — stack_1a2b3c · 2026-08-11T09:14:22Z
            </p>
          </Specimen>

          <Specimen label="Scale" stack>
            <dl className="sg__rows">
              {TYPE_STEPS.map((step) => (
                <div className="sg__row" key={step}>
                  <dt className="sg__row-name">
                    {step} · {type[step] || "—"}
                  </dt>
                  <dd style={{ margin: 0, fontSize: `var(${step})`, lineHeight: "var(--leading-tight)" }}>
                    Terraform
                  </dd>
                </div>
              ))}
            </dl>
          </Specimen>
        </Section>

        <Section id="radii" title="Radii">
          <Specimen label="Scale">
            {RADIUS_TOKENS.map((token) => (
              <div key={token} style={{ display: "grid", gap: "var(--space-2)", justifyItems: "center" }}>
                <div className="sg__radius-tile" style={{ borderRadius: `var(${token})` }} />
                <span className="sg__row-name">{token}</span>
                <span className="sg__row-name">{radii[token] || "—"}</span>
              </div>
            ))}
          </Specimen>
        </Section>

        <Section
          id="shadows"
          title="Shadows"
          note="Every box-shadow in the codebase must reference one of these tokens; a guard test fails the build otherwise."
        >
          <Specimen label="Scale" stack>
            <dl className="sg__rows">
              {SHADOW_TOKENS.map((token) => (
                <div className="sg__row" key={token}>
                  <dt className="sg__row-name">{token}</dt>
                  <dd style={{ margin: 0 }}>
                    <div className="sg__shadow-tile" style={{ boxShadow: `var(${token})` }} />
                  </dd>
                </div>
              ))}
            </dl>
          </Specimen>
        </Section>

        <Section
          id="buttons"
          title="Buttons"
          note="Primary carries the gradient and lifts on hover. Destructive stays neutral-weight until a confirm step sets data-confirming, so the red never becomes ambient."
        >
          <Specimen label="Variants" hint="hover and focus them">
            <button type="button" className="primary-button">
              Run plan <span className="btn-arrow">→</span>
            </button>
            <button type="button" className="secondary-button">
              Add grant
            </button>
            <button type="button" className="destructive-button">
              Destroy <span className="btn-arrow">→</span>
            </button>
            <button type="button" className="icon-button" aria-label="Remove">
              ✕
            </button>
          </Specimen>

          <Specimen label="States">
            <button type="button" className="primary-button" disabled>
              Disabled
            </button>
            <button type="button" className="destructive-button" data-confirming="true">
              Confirm destroy
            </button>
          </Specimen>
        </Section>

        <Section id="inputs" title="Inputs" note="Focus draws a two-step accent ring rather than recolouring the field.">
          <Specimen label="Fields" stack>
            <label style={{ maxWidth: "360px" }}>
              Stack name
              <input placeholder="payments-core" />
            </label>
            <label style={{ maxWidth: "360px", marginTop: "var(--space-5)" }}>
              Description
              <textarea placeholder="What does this stack manage?" />
            </label>
            <label style={{ maxWidth: "360px", marginTop: "var(--space-5)" }}>
              Role
              <select defaultValue="operator">
                <option value="owner">owner</option>
                <option value="operator">operator</option>
                <option value="approver">approver</option>
                <option value="viewer">viewer</option>
              </select>
            </label>
          </Specimen>
        </Section>

        <Section
          id="panels"
          title="Panels"
          note="The featured variant paints its gradient border from a background layer, so it carries a forced-colors fallback; without one it would lose all emphasis in Windows High Contrast."
        >
          <Specimen label="Variants" stack>
            <div style={{ display: "grid", gap: "var(--space-5)" }}>
              <section className="panel">
                <h2>Standard</h2>
                <p className="muted">One border, one shadow, rounded corners.</p>
              </section>
              <section className="panel panel--featured">
                <h2>Featured</h2>
                <p className="muted">Gradient border, drawn with no wrapper element.</p>
              </section>
              <section className="panel panel--inverted">
                <h2>Inverted</h2>
                <p>Dark surface with a dot texture, used for emphasis bands.</p>
              </section>
            </div>
          </Specimen>
        </Section>

        <Section
          id="status"
          title="Status tones"
          note="Thirty status values across three API unions map onto five tones via statusTone(). Each pill pairs a glyph with the literal status text, so colour is never the only carrier of meaning."
        >
          <Specimen label="StatusRow" hint="real component" stack>
            <div style={{ maxWidth: "480px" }}>
              <StatusRow label="Template" value="completed" />
              <StatusRow label="Plan" value="plan_started" />
              <StatusRow label="Approval" value="waiting_approval" />
              <StatusRow label="Apply" value="failed" />
              <StatusRow label="Cancelled" value="canceled" />
              <StatusRow label="Destroy" value="destroy_finished" />
            </div>
          </Specimen>
        </Section>

        <Section id="roles" title="Role badges" note="Tinted pills; the tint encodes the role, the text always states it.">
          <Specimen label="Variants">
            <span className="role-badge role-badge--owner">owner</span>
            <span className="role-badge role-badge--operator">operator</span>
            <span className="role-badge role-badge--approver">approver</span>
            <span className="role-badge role-badge--viewer">viewer</span>
          </Specimen>
        </Section>

        <Section id="stats" title="Stat band">
          <Specimen label="StatBand" hint="real component" stack>
            <StatBand
              items={[
                { label: "Stacks", value: 4 },
                { label: "You can operate", value: 3 }
              ]}
            />
          </Specimen>
        </Section>

        <Section id="messaging" title="Messaging">
          <Specimen label="Variants" stack>
            <div className="alert">Could not reach the authorization service.</div>
            <p className="error-text">Credential TF_VAR_token is not configured</p>
            <p className="muted">Secondary text uses the muted foreground.</p>
          </Specimen>
        </Section>

        <Section id="tabs" title="Tabs">
          <Specimen label="Active state" hint="gradient underline">
            <div className="stack-detail-tabs" style={{ marginBottom: 0 }}>
              <a href="#tabs" className="active">
                Overview
              </a>
              <a href="#tabs">Template</a>
              <a href="#tabs">Runs</a>
              <a href="#tabs">Access</a>
            </div>
          </Specimen>
        </Section>

        <Section
          id="log"
          title="Log panel"
          note="Deliberately carries no texture. Patterning behind a monospace log stream measurably hurts scanning for errors."
        >
          <Specimen label="log-panel" stack>
            <div className="log-panel">
              <pre>{`Initializing the backend...
Terraform v1.9.5 on darwin_arm64
Plan: 3 to add, 1 to change, 0 to destroy.
Error: creating S3 Bucket: BucketAlreadyExists`}</pre>
            </div>
          </Specimen>
        </Section>

        <Section
          id="showpiece"
          title="Showpieces"
          note="The hero graphic is decorative and aria-hidden. Its ring rotates over 60 seconds and its cards drift on offset timings; all of it stops under prefers-reduced-motion."
        >
          <Specimen label="HeroGraphic" hint="real component">
            <HeroGraphic />
          </Specimen>

          <Specimen label="showcase" stack>
            <section className="showcase" style={{ minHeight: 0 }}>
              <div className="showcase__body">
                <h1 className="showcase__title gradient-text">Authorization service unavailable</h1>
                <p className="showcase__lede">
                  We could not reach the authorization service. Retry in a moment.
                </p>
                <a className="secondary-button" href="#showpiece">
                  Back to stacks
                </a>
              </div>
              <div className="showcase__visual">
                <HeroGraphic />
              </div>
            </section>
          </Specimen>
        </Section>
      </main>
    </div>
  );
}
