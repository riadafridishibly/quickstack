import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig, type Plugin } from "vite";

// The app in `make dev`: templ's proxy in front of the Go server.
const appURL = "http://localhost:7360";

// This dev server only serves CSS/JS to pages rendered by Go; it has no pages
// of its own. Print the app URL instead of Vite's, and send / to the app.
function pointToApp(): Plugin {
  return {
    name: "point-to-app",
    apply: "serve",
    configureServer(server) {
      server.printUrls = () => {
        server.config.logger.info(
          `  ➜  App:    ${appURL}  ← open this\n` +
            `  ➜  Assets: http://localhost:${server.config.server.port} (Vite, used by the app)\n`,
        );
      };
      server.middlewares.use((req, res, next) => {
        if (req.url !== "/") return next();
        res.writeHead(302, { Location: appURL });
        res.end();
      });
    },
  };
}

// The build goes to internal/web/dist, which the Go binary embeds and serves
// under /static/. In dev, the Go layout loads /@vite/client and /src/main.ts
// straight from this server (see internal/web/assets.go).
export default defineConfig(({ command }) => ({
  root: "frontend",
  base: command === "build" ? "/static/" : "/",
  plugins: [tailwindcss(), pointToApp()],
  server: {
    port: 8361,
    strictPort: true,
    origin: "http://localhost:8361",
  },
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
    manifest: true,
    rolldownOptions: {
      // Stylesheets are their own entries so layouts can <link> them in dev
      // too: app.css for the dashboards, public.css for the public pages.
      input: ["./frontend/src/main.ts", "./frontend/src/app.css", "./frontend/src/public.css"].map(
        (p) => fileURLToPath(new URL(p, import.meta.url)),
      ),
      onLog(level, log, handler) {
        // htmx only evals hx-on:* / js: expressions, which main.ts disables.
        if (log.code === "EVAL" && log.id?.includes("htmx.org")) return;
        handler(level, log);
      },
    },
  },
}));
