const getOidcConfig = () => {
  const origin = globalThis.location?.origin ?? "http://localhost:5173";
  const envRedirectUri = import.meta.env.VITE_OIDC_REDIRECT_URI;
  const redirectUri = typeof envRedirectUri === "string" && envRedirectUri.length > 0
    ? envRedirectUri
    : `${origin}/auth/callback`;

  return {
    authority: import.meta.env.VITE_OIDC_ISSUER ?? "http://localhost:8082/realms/tflive",
    client_id: import.meta.env.VITE_OIDC_CLIENT_ID ?? "tflive-web",
    redirect_uri: redirectUri,
    post_logout_redirect_uri: redirectUri,
    response_type: "code",
    scope: "openid profile email",
    useRefreshToken: true,
    loadUserInfo: false,
  } as const;
};

export const oidcConfig = getOidcConfig();
