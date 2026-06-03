const path = require('node:path');

const ROOT = path.resolve(__dirname, '..', '..');
const DIST_DIR = path.join(ROOT, 'dist');
const PACKAGES_DIR = path.join(ROOT, 'npm');
const NPM_SCOPE = '@ricequant2026';

const PLATFORM_TARGETS = [
  {
    key: 'linux-x64',
    goos: 'linux',
    goarch: 'amd64',
    os: 'linux',
    cpu: 'x64',
    packageName: `${NPM_SCOPE}/rqams-cli-linux-x64`,
    packageDir: path.join(PACKAGES_DIR, 'rqams-cli-linux-x64'),
    output: 'rqamsc-linux-amd64',
    binName: 'rqamsc'
  },
  {
    key: 'darwin-x64',
    goos: 'darwin',
    goarch: 'amd64',
    os: 'darwin',
    cpu: 'x64',
    packageName: `${NPM_SCOPE}/rqams-cli-darwin-x64`,
    packageDir: path.join(PACKAGES_DIR, 'rqams-cli-darwin-x64'),
    output: 'rqamsc-macos-amd64',
    binName: 'rqamsc'
  },
  {
    key: 'darwin-arm64',
    goos: 'darwin',
    goarch: 'arm64',
    os: 'darwin',
    cpu: 'arm64',
    packageName: `${NPM_SCOPE}/rqams-cli-darwin-arm64`,
    packageDir: path.join(PACKAGES_DIR, 'rqams-cli-darwin-arm64'),
    output: 'rqamsc-macos-arm64',
    binName: 'rqamsc'
  },
  {
    key: 'win32-x64',
    goos: 'windows',
    goarch: 'amd64',
    os: 'win32',
    cpu: 'x64',
    packageName: `${NPM_SCOPE}/rqams-cli-win32-x64`,
    packageDir: path.join(PACKAGES_DIR, 'rqams-cli-win32-x64'),
    output: 'rqamsc-windows-amd64.exe',
    binName: 'rqamsc.exe'
  }
];

function currentPlatformKey() {
  return `${process.platform}-${process.arch}`;
}

function findCurrentTarget() {
  return PLATFORM_TARGETS.find((target) => target.key === currentPlatformKey()) || null;
}

function findTargetByKey(key) {
  return PLATFORM_TARGETS.find((target) => target.key === key) || null;
}

module.exports = {
  DIST_DIR,
  NPM_SCOPE,
  PACKAGES_DIR,
  PLATFORM_TARGETS,
  findCurrentTarget,
  findTargetByKey
};
