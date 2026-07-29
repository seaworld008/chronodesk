import {
    closeSync,
    constants,
    fstatSync,
    openSync,
    readFileSync,
    readdirSync,
} from 'node:fs';
import { createRequire } from 'node:module';
import { extname, join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const allowedAdvisory = 'https://github.com/advisories/GHSA-qwww-vcr4-c8h2';
const exceptionExpiresAt = new Date('2026-09-30T00:00:00Z');
const expectedRouterVersion = '7.18.2';
const sourceExtensions = new Set(['.js', '.jsx', '.mjs', '.ts', '.tsx']);
const unstableRSCMarkers = [
    'RSCRouter',
    'RSCStaticRouter',
    'createCallServer',
    'decodeReply',
    'react-server',
    'unstable_RSC',
];
const unsafeTrustedDeviceCredentialPatterns = [
    /localStorage\.(?:getItem|setItem)\(\s*['"]trustedDeviceToken['"]/,
    /\bdevice_token\b/,
    /\btrusted_device_token\b/,
];

const fail = (message) => {
    console.error(`依赖安全审计失败：${message}`);
    process.exit(1);
};

if (Date.now() >= exceptionExpiresAt.getTime()) {
    fail('React Router RSC 安全例外已到期，请升级至 React Admin 官方支持的修复版本');
}

const routerDOMPackageURL = new URL(
    '../node_modules/react-router-dom/package.json',
    import.meta.url,
);
const routerDOMPackage = JSON.parse(readFileSync(routerDOMPackageURL, 'utf8'));
const routerDependencyName = Object.keys(routerDOMPackage.dependencies ?? {}).find(
    (name) => name === 'react-router',
);
if (!routerDependencyName) {
    fail('react-router-dom 未声明 react-router 运行时依赖');
}
const requireFromRouterDOM = createRequire(routerDOMPackageURL);
const routerPackage = JSON.parse(
    readFileSync(
        requireFromRouterDOM.resolve(`${routerDependencyName}/package.json`),
        'utf8',
    ),
);
if (routerPackage.version !== expectedRouterVersion) {
    fail(
        `React Router 实际版本为 ${routerPackage.version}，安全例外仅适用于 ${expectedRouterVersion}`,
    );
}

const readRegularFileWithoutFollowingLinks = (target) => {
    let descriptor;
    try {
        descriptor = openSync(target, constants.O_RDONLY | constants.O_NOFOLLOW);
        if (!fstatSync(descriptor).isFile()) {
            fail(`安全审计只允许读取普通文件（${target}）`);
        }
        return readFileSync(descriptor, 'utf8');
    } catch (error) {
        fail(`无法安全读取源文件 ${target}：${error.message}`);
    } finally {
        if (descriptor !== undefined) {
            closeSync(descriptor);
        }
    }
};

const scanFiles = (directory) => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
        const target = join(directory, entry.name);
        if (entry.isSymbolicLink()) {
            fail(`源码目录不允许符号链接，以免绕过安全审计（${target}）`);
        }
        if (entry.isDirectory()) {
            scanFiles(target);
            continue;
        }
        if (!entry.isFile()) {
            continue;
        }
        if (!sourceExtensions.has(extname(target))) {
            continue;
        }
        const source = readRegularFileWithoutFollowingLinks(target);
        const marker = unstableRSCMarkers.find((candidate) => source.includes(candidate));
        if (marker) {
            fail(`检测到不在安全例外范围内的 React Router RSC 标记 ${marker}（${target}）`);
        }
        const unsafeCredentialPattern = unsafeTrustedDeviceCredentialPatterns.find(
            (candidate) => candidate.test(source),
        );
        if (unsafeCredentialPattern) {
            fail(`可信设备凭据不得进入前端脚本、JSON 或 Web Storage（${target}）`);
        }
    }
};

scanFiles(fileURLToPath(new URL('../src', import.meta.url)));

const result = spawnSync('npm', ['audit', '--omit=dev', '--json'], {
    cwd: new URL('..', import.meta.url),
    encoding: 'utf8',
});
if (result.error) {
    fail(`无法执行 npm audit：${result.error.message}`);
}

let report;
try {
    report = JSON.parse(result.stdout);
} catch {
    fail(`npm audit 未返回有效 JSON：${result.stderr.trim() || '无诊断信息'}`);
}

const vulnerabilities = report.vulnerabilities ?? {};
const names = Object.keys(vulnerabilities).sort();
const expectedNames = ['react-router', 'react-router-dom'];
if (
    names.length !== expectedNames.length ||
    names.some((name, index) => name !== expectedNames[index])
) {
    fail(`发现未获批准的生产依赖漏洞：${names.join(', ') || '未知'}`);
}

const routerVulnerability = vulnerabilities['react-router'];
const routerDOMVulnerability = vulnerabilities['react-router-dom'];
const routerAdvisories = (routerVulnerability?.via ?? []).filter(
    (item) => typeof item === 'object',
);
if (
    routerVulnerability?.severity !== 'high' ||
    routerAdvisories.length !== 1 ||
    routerAdvisories[0]?.url !== allowedAdvisory ||
    routerDOMVulnerability?.severity !== 'high' ||
    JSON.stringify(routerDOMVulnerability?.via) !== JSON.stringify(['react-router'])
) {
    fail('React Router 漏洞集合已变化，必须重新进行安全评估');
}

console.log(
    `生产依赖审计通过：仅保留 ${allowedAdvisory}；官方确认其只影响未启用的 unstable RSC API，例外复核期限为 2026-09-30`,
);
