# Publishing pi-solo

## Before first publish

1. Make sure you are logged into npm:
   ```bash
   npm whoami
   ```
   If needed:
   ```bash
   npm login
   ```

2. Verify the package name is still available:
   ```bash
   npm view pi-solo name version
   ```
   A 404 means the package name is still unclaimed.

3. Run the local package checks:
   ```bash
   cd pi-extension
   npm install
   npm run publish:check
   ```

## First publish

```bash
cd pi-extension
npm publish
```

## Publish an update

1. Bump the version:
   ```bash
   cd pi-extension
   npm version patch
   ```
   Use `minor` for new user-facing features and `major` for breaking changes.

2. Publish:
   ```bash
   npm publish
   ```

## Post-publish verification

1. Confirm npm sees the new version:
   ```bash
   npm view pi-solo version
   ```

2. Confirm pi can install it:
   ```bash
   pi install npm:pi-solo
   ```

3. Confirm the package metadata is still correct:
   - `keywords` includes `pi-package`
   - `pi.extensions` points at `./src/index.ts`

## Notes

- The package is unscoped, so `npm publish` is sufficient.
- If you later move to a scoped name like `@your-scope/pi-solo`, add:
  ```json
  "publishConfig": {
    "access": "public"
  }
  ```
- The package gallery reads npm package metadata. The important fields are:
  - `keywords: ["pi-package"]`
  - `pi.extensions`
  - a useful `README.md`
