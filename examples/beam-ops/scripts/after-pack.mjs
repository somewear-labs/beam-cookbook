// afterPack hook for electron-builder.
// Copies the platform-appropriate rpc binary into the packed output directory
// BEFORE electron-builder seals the AppImage / zip. This is necessary because
// extraResources is unreliable when cross-compiling Linux AppImages on macOS.
import { copyFileSync, mkdirSync, readdirSync, existsSync } from 'fs';
import { join } from 'path';

export default async function afterPack({ appOutDir, packager, electronPlatformName }) {
  const srcDir = join(packager.projectDir, '..', '..', '..', 'beam-cookbook', 'rpc', 'bin');
  if (!existsSync(srcDir)) {
    console.warn('[after-pack] beam-cookbook/rpc/bin not found — skipping');
    return;
  }

  let resourcesDir;
  if (electronPlatformName === 'linux') {
    resourcesDir = join(appOutDir, 'resources');
  } else if (electronPlatformName === 'darwin') {
    resourcesDir = join(appOutDir, packager.appInfo.productName + '.app', 'Contents', 'Resources');
  } else {
    return;
  }

  const destDir = join(resourcesDir, 'beam-cookbook', 'rpc', 'bin');
  mkdirSync(destDir, { recursive: true });

  const prefix = electronPlatformName === 'linux' ? 'rpc_linux_' : 'rpc_darwin_';
  let count = 0;
  for (const f of readdirSync(srcDir)) {
    if (f.startsWith(prefix)) {
      copyFileSync(join(srcDir, f), join(destDir, f));
      count++;
    }
  }
  console.log(`[after-pack] copied ${count} rpc binary/binaries → ${destDir}`);
}
