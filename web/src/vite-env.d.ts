/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_TFLIVE_TENANT_ID?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
