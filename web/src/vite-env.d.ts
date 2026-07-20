/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_TFLIVE_TENANT_ID?: string;
  readonly VITE_TFLIVE_MOCK_USER_ROLE?: string;
  readonly VITE_DEBUG?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
