# Traefik Rybbit Feeder Plugin

A [Traefik](https://traefik.io/traefik/) middleware plugin that sends visits to your [Rybbit](https://www.rybbit.io/) instance.

**This is a fork of [traefik-umami-feeder](https://github.com/astappiev/traefik-umami-feeder) modified to work with Rybbit.**

## Introduction

This plugin integrates your Traefik-proxied services with Rybbit, a simple, fast, privacy-focused analytics solution. It
captures basic request information (path, user-agent, referrer, IP) and forwards it to your Rybbit instance,
enabling server-side analytics.

Key features:

- Stupidly simple to setup, one middleware for all websites possible
- Server-Side Tracking, no JS or Cookies bullshit
- Fast and private

## Configuration

### Step 1. Add the plugin to Traefik

Declare the plugin in your Traefik **static configuration**.

```yaml
experimental:
  plugins:
    rybbit-feeder:
      moduleName: github.com/foxxmd/traefik-rybbit-feeder
      version: v0.13.0 # Replace with the latest version
```

### Step 2. Configure the middleware

Once the plugin is declared, configure it as a middleware in your Traefik **dynamic configuration**.

You will need to specify at least three pieces of data:

* `host` - Your Rybbit instance base URL. This is URL **without** `/api/track`.
* `apiKey` - The [**Api Key**](https://www.rybbit.io/docs/api#steps) generated for the website you wish to track.
* `websites` - A map of `domain` - `site-id` properties to tell the feeder what traffic should go to your Rybbit Site

There are additional, optional [Middleware Options](#middleware-options) to configure more behavior below.

```yaml
http:
  middlewares:
    my-rybbit-middleware:
      plugin:
        rybbit-feeder:
          host: "http://rybbit.mysite.com" # URL of your Rybbit instance

          apiKey: rb_a0938250c2c2efd061c8250c2c3707a

          websites:
            # domain to capture traffic for and the site-id from Rybbit
            "example.com": "1"
```

### Step 3. Attach the middleware to your routers

Apply the [configured middleware](https://doc.traefik.io/traefik/routing/routers/#middlewares_1) to the Traefik routers
you want to track with Rybbit. This is also done in your **dynamic configuration**.

Remember to use the
correct [provider namespace](https://doc.traefik.io/traefik/providers/overview/#provider-namespace)  (e.g., `@file` if
your middleware is defined in a file, `@docker` if defined via Docker labels).

**Example using Docker labels:**

```yaml
- "traefik.http.routers.whoami.middlewares=my-rybbit-middleware@file"
```

**Example using a dynamic configuration file (e.g., `dynamic_conf.yml`):**

```yaml
http:
  routers:
    whoami:
      rule: "Host(`example.com`)"
      middlewares:
        - my-rybbit-middleware@file
```

**Example using static configuration (e.g., `traefik.yml`), by attaching the middleware to an entryPoint to apply it
globally:**

```yaml
entryPoints:
  web:
    http:
      middlewares:
        - my-rybbit-middleware@file
```

## Middleware Options

| key                 | default         | type       | description                                                                                                                                                                                  |
| ------------------- | :-------------- | :--------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `disabled`          | `false`         | `bool`     | Set to `true` to disable the plugin.                                                                                                                                                         |
| `debug`             | `false`         | `bool`     | Set to `true` for verbose logging. Useful for troubleshooting as plugins don't inherit Traefik's global log level.                                                                           |
| `host`              | **required**    | `string`   | URL of your Rybbit instance, reachable from Traefik (e.g., `https://rybbit.mydomain.com`).                                                                                                   |
| `apiKey`            | **required**    | `string`   | [Rybbit API Key](https://www.rybbit.io/docs/api#steps) for authenticating with your Rybbit instance.                                                                                         |
| `websites`          | **required**    | `map`      | A map of `hostname: site-id`                                                                                                                                                                 |
| `trackErrors`       | `false`         | `bool`     | If `true`, tracks errors (status codes >= 400).                                                                                                                                              |
| `trackAllResources` | `false`         | `bool`     | If `true`, tracks requests for all resources. By default, only requests likely to be page views (e.g., HTML, or no specific extension) are tracked.                                          |
| `trackExtensions`   | `[see sources]` | `string[]` | A list of specific file extensions to track (e.g., `[".html", ".php"]`).                                                                                                                     |
| `ignoreUserAgents`  | `[]`            | `string[]` | A list of user-agent substrings. Requests with matching user-agents will be ignored (e.g., `["Googlebot", "Uptime-Kuma"]`). Matched with `strings.Contains`.                                 |
| `ignoreURLs`        | `[]`            | `string[]` | A list of regular expressions. Requests with URLs matching any of these patterns will be ignored (e.g., `["/health", "https?://[^/]+/health$"]`). Matched with `regexp.Compile.MatchString`. |
| `ignoreIPs`         | `[]`            | `string[]` | A list of IP addresses or CIDR ranges to ignore (e.g., `["127.0.0.1", "10.0.0.1/16"]`). Matched with `netip.ParsePrefix.Contains`.                                                           |
| `headerIp`          | `X-Real-Ip`     | `string`   | The HTTP header to inspect for the client's real IP address, typically used when Traefik is behind another proxy.                                                                            |

## Contributing

Contributions are welcome! Please feel free to submit a pull request or open an issue.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
