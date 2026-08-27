(function () {
  "use strict";

  var config = window.__DOCS_CONFIG__;
  var renderer = document.body.dataset.renderer;
  var root = document.getElementById("renderer-root");

  document.querySelectorAll("[data-api-name]").forEach(function (element) {
    element.textContent = config.name;
  });
  document.querySelector("[data-spec-json]").href = config.specJSON;
  document.querySelector("[data-spec-yaml]").href = config.specYAML;
  document.title = config.name + " API - " + document.title.split(" - ")[0];

  render(renderer).catch(function (error) {
    root.innerHTML = "";
    var message = document.createElement("div");
    message.className = "load-error";
    message.setAttribute("role", "alert");
    message.textContent = "Documentation renderer failed to load.";
    root.appendChild(message);
    console.error(error);
  });

  async function render(name) {
    root.innerHTML = "";
    switch (name) {
      case "scalar": {
        var scalar = document.createElement("script");
        scalar.id = "api-reference";
        scalar.dataset.url = config.specJSON;
        scalar.src = "/docs/assets/scalar.js";
        root.appendChild(scalar);
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
      default:
        throw new Error("Unknown documentation renderer: " + name);
    }
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
