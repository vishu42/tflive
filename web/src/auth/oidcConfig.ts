export const oidcConfig = {
  authority: import.meta.env.VITE_OIDC_ISSUER ?? "http://localhost:8082/realms/tflive",
  client_id: import.meta.env.VITE_OIDC_CLIENT_ID ?? "tflive-web",
  redirect_uri: import.meta.env.VITE_OIDC_REDIRECT_URI ?? "http://localhost:5173/auth/callback",
  post_logout_redirect_uri: import.meta.env.VITE_OIDC_REDIRECT_URI ?? "http://localhost:5173/auth/callback",
  response_type: "code",
  scope: "openid profile email",
  useRefreshToken: true,
  loadUserInfo: false,
} as const;
