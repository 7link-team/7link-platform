import { parse as parseYAML } from "yaml";

(function () {
  "use strict";

  var config = window.__DOCS_CONFIG__;
  var serviceName = String(config.name || "").trim();
  var renderer = document.body.dataset.renderer;
  var root = document.getElementById("renderer-root");

  document.querySelectorAll("[data-api-mark]").forEach(function (element) {
    var mark = Array.from(serviceName)[0];
    if (mark) element.textContent = mark.toUpperCase();
  });

  document.querySelectorAll("[data-api-name]").forEach(function (element) {
    element.textContent = serviceName;
  });
  document.title = serviceName + " API - " + document.title.split(" - ")[0];

  render(renderer).catch(function (error) {
    root.innerHTML = "";
    var message = document.createElement("div");
    message.className = "load-error";
    message.setAttribute("role", "alert");
    message.textContent = "Documentation renderer failed to load.";
    root.appendChild(message);
    var retry = document.createElement("a");
    retry.className = "retry-link";
    retry.href = window.location.href;
    retry.textContent = "Reload";
    root.appendChild(retry);
    console.error(error);
  });

  async function render(name) {
    root.innerHTML = "";
    switch (name) {
      case "scalar": {
        await loadScript("/docs/assets/scalar.js");
        window.Scalar.createApiReference(root, { url: config.specJSON });
        return;
      }
      case "swagger": {
        loadStylesheet("/docs/assets/swagger-ui.css");
        await loadScript("/docs/assets/swagger-ui-bundle.js");
        var swagger = document.createElement("div");
        swagger.id = "swagger-ui";
        root.appendChild(swagger);
        window.ui = window.SwaggerUIBundle({ url: config.specJSON, dom_id: "#swagger-ui" });
        return;
      }
      case "redoc": {
        await loadScript("/docs/assets/redoc.js");
        var redoc = document.createElement("div");
        root.appendChild(redoc);
        window.Redoc.init(config.specJSON, {}, redoc);
        return;
      }
      case "rapidoc": {
        await loadScript("/docs/assets/rapidoc.js", "module");
        var rapidoc = document.createElement("rapi-doc");
        rapidoc.setAttribute("spec-url", config.specJSON);
        rapidoc.setAttribute("theme", "light");
        rapidoc.setAttribute("render-style", "read");
        rapidoc.setAttribute("show-header", "false");
        root.appendChild(rapidoc);
        return;
      }
      case "stoplight": {
        loadStylesheet("/docs/assets/stoplight.css");
        await loadScript("/docs/assets/stoplight.js");
        var stoplight = document.createElement("elements-api");
        stoplight.apiDescriptionUrl = config.specYAML;
        stoplight.router = "hash";
        stoplight.layout = "sidebar";
        stoplight.tryItCredentialsPolicy = "same-origin";
        root.appendChild(stoplight);
        return;
      }
      case "openapi-ui": {
        var openapi = document.createElement("div");
        openapi.id = "openapi-ui-container";
        openapi.setAttribute("spec-url", config.specJSON);
        openapi.setAttribute("theme", "light");
        root.appendChild(openapi);
        await loadScript("/docs/assets/openapi-ui.js");
        return;
      }
      case "openapi-json":
        await renderSpec("json", config.specJSON);
        return;
      case "openapi-yaml":
        await renderSpec("yaml", config.specYAML);
        return;
      default:
        throw new Error("Unknown documentation renderer: " + name);
    }
  }

  async function renderSpec(format, source) {
    var response = await fetch(source, {
      headers: { Accept: format === "json" ? "application/openapi+json" : "application/openapi+yaml" }
    });
    if (!response.ok) throw new Error("OpenAPI request failed: HTTP " + response.status);

    var text = await response.text();
    var spec = format === "json" ? JSON.parse(text) : parseYAML(text);

    var viewer = document.createElement("section");
    viewer.className = "spec-viewer";
    var toolbar = document.createElement("header");
    toolbar.className = "spec-toolbar";
    var filename = document.createElement("strong");
    filename.textContent = "openapi." + format;
    var actions = document.createElement("div");
    actions.className = "spec-actions";
    var expand = actionButton("Expand all", function () {
      viewer.querySelectorAll("details").forEach(function (item) { item.open = true; });
    });
    var collapse = actionButton("Collapse all", function () {
      viewer.querySelectorAll("details").forEach(function (item) { item.open = false; });
    });
    var download = document.createElement("a");
    download.className = "download-link";
    download.href = source;
    download.download = "openapi." + format;
    download.textContent = "Download";
    actions.append(expand, collapse, download);
    toolbar.append(filename, actions);

    var tree = document.createElement("div");
    tree.className = "spec-tree";
    appendTree(tree, spec, "OpenAPI", 0);
    viewer.append(toolbar, tree);
    root.appendChild(viewer);
  }

  function actionButton(label, action) {
    var button = document.createElement("button");
    button.type = "button";
    button.className = "tree-action";
    button.textContent = label;
    button.addEventListener("click", action);
    return button;
  }

  function appendTree(parent, value, key, depth) {
    if (value !== null && typeof value === "object") {
      var entries = Object.entries(value);
      var details = document.createElement("details");
      details.open = depth < 2;
      var summary = document.createElement("summary");
      var name = document.createElement("span");
      name.className = "tree-key";
      name.textContent = key;
      var count = document.createElement("span");
      count.className = "tree-count";
      count.textContent = Array.isArray(value)
        ? "Array(" + entries.length + ")"
        : "Object(" + entries.length + ")";
      summary.append(name, count);
      details.appendChild(summary);
      var children = document.createElement("div");
      children.className = "tree-children";
      entries.forEach(function (entry) {
        appendTree(children, entry[1], entry[0], depth + 1);
      });
      details.appendChild(children);
      parent.appendChild(details);
      return;
    }

    var row = document.createElement("div");
    row.className = "tree-leaf";
    var leafKey = document.createElement("span");
    leafKey.className = "tree-key";
    leafKey.textContent = key;
    var leafValue = document.createElement("span");
    leafValue.className = "tree-value tree-value-" + (value === null ? "null" : typeof value);
    leafValue.textContent = typeof value === "string" ? JSON.stringify(value) : String(value);
    row.append(leafKey, leafValue);
    parent.appendChild(row);
  }

  function loadScript(source, type) {
    return new Promise(function (resolve, reject) {
      var script = document.createElement("script");
      script.src = source;
      if (type) script.type = type;
      script.onload = resolve;
      script.onerror = reject;
      document.head.appendChild(script);
    });
  }

  function loadStylesheet(source) {
    var link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = source;
    document.head.appendChild(link);
  }
})();
