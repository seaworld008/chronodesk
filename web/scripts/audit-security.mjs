import { spawnSync } from 'node:child_process';
import {
    closeSync,
    constants,
    fstatSync,
    openSync,
    readFileSync,
    readdirSync,
} from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, extname, join, resolve } from 'node:path';
import { isDeepStrictEqual } from 'node:util';
import { fileURLToPath } from 'node:url';

export const staleAdvisory =
    'https://github.com/advisories/GHSA-qwww-vcr4-c8h2';
export const expectedRouterVersion = '7.18.2';
export const expectedBackportEntry =
    '## v7.18.2\n\n### Patch Changes\n\n- Harden RSC CSRF codepaths. ([#15353]';

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
const expectedVulnerabilityNames = ['react-router', 'react-router-dom'];
const cleanVulnerabilityMetadata = {
    info: 0,
    low: 0,
    moderate: 0,
    high: 0,
    critical: 0,
    total: 0,
};
const exactFalsePositiveMetadata = {
    ...cleanVulnerabilityMetadata,
    high: 2,
    total: 2,
};
const expectedFixAvailable = {
    name: 'react-router-dom',
    version: '7.11.0',
    isSemVerMajor: true,
};
const expectedFalsePositiveVulnerabilities = {
    'react-router': {
        name: 'react-router',
        severity: 'high',
        isDirect: false,
        via: [
            {
                source: 1124282,
                name: 'react-router',
                dependency: 'react-router',
                title: 'React Router: RSC Mode CSRF Bypass Allows Action Execution Before 400 Response',
                url: staleAdvisory,
                severity: 'high',
                cwe: ['CWE-352'],
                cvss: {
                    score: 0,
                    vectorString: null,
                },
                range: '>=7.12.0 <8.3.0',
            },
        ],
        effects: ['react-router-dom'],
        range: '7.12.0 - 8.2.0',
        nodes: ['node_modules/react-router'],
        fixAvailable: expectedFixAvailable,
    },
    'react-router-dom': {
        name: 'react-router-dom',
        severity: 'high',
        isDirect: true,
        via: ['react-router'],
        effects: [],
        range: '>=7.12.0-pre.0',
        nodes: ['node_modules/react-router-dom'],
        fixAvailable: expectedFixAvailable,
    },
};

export class SecurityAuditError extends Error {
    constructor(message) {
        super(message);
        this.name = 'SecurityAuditError';
    }
}

const reject = (message) => {
    throw new SecurityAuditError(message);
};

const isRecord = (value) =>
    value !== null && typeof value === 'object' && !Array.isArray(value);

const diagnostic = (stderr) =>
    typeof stderr === 'string' && stderr.trim() !== ''
        ? stderr.trim()
        : '无诊断信息';

const assertAuditReportShape = (report, stderr = '') => {
    if (
        !isRecord(report) ||
        report.auditReportVersion !== 2 ||
        !isRecord(report.vulnerabilities) ||
        !isRecord(report.metadata) ||
        !isRecord(report.metadata.vulnerabilities) ||
        !Number.isInteger(report.metadata.vulnerabilities.total) ||
        report.metadata.vulnerabilities.total < 0
    ) {
        reject(`npm audit 返回结构不完整：${diagnostic(stderr)}`);
    }
    return report;
};

export const validateRouterInstallation = ({
    routerVersion,
    routerChangelog,
}) => {
    if (routerVersion !== expectedRouterVersion) {
        reject(
            `React Router 实际版本为 ${routerVersion ?? '未知'}，要求使用包含 v7 回移修复的 ${expectedRouterVersion}`,
        );
    }
    if (
        typeof routerChangelog !== 'string' ||
        !routerChangelog.includes(expectedBackportEntry)
    ) {
        reject('React Router 7.18.2 安装包缺少官方 #15353 RSC CSRF 回移修复记录');
    }
    return expectedRouterVersion;
};

export const parseAuditReport = (stdout, stderr = '') => {
    let report;
    try {
        report = JSON.parse(stdout);
    } catch {
        reject(`npm audit 未返回有效 JSON：${diagnostic(stderr)}`);
    }
    return assertAuditReportShape(report, stderr);
};

export const validateAuditReport = (report, status) => {
    assertAuditReportShape(report);
    if (!Number.isInteger(status)) {
        reject('npm audit 未正常退出，无法确认生产依赖安全状态');
    }

    const vulnerabilities = report.vulnerabilities;
    const names = Object.keys(vulnerabilities).sort();
    const vulnerabilityMetadata = report.metadata.vulnerabilities;

    if (names.length === 0) {
        if (
            status !== 0 ||
            !isDeepStrictEqual(
                vulnerabilityMetadata,
                cleanVulnerabilityMetadata,
            )
        ) {
            reject('npm audit 的零漏洞结果与退出状态不一致');
        }
        return {
            kind: 'clean',
            message: `生产依赖审计通过：React Router ${expectedRouterVersion} 已包含官方 #15353 回移修复，且 npm audit 未发现生产依赖漏洞`,
        };
    }

    if (
        names.length !== expectedVulnerabilityNames.length ||
        names.some(
            (name, index) => name !== expectedVulnerabilityNames[index],
        )
    ) {
        reject(`发现未获批准的生产依赖漏洞：${names.join(', ') || '未知'}`);
    }

    if (
        status !== 1 ||
        !isDeepStrictEqual(
            vulnerabilityMetadata,
            exactFalsePositiveMetadata,
        ) ||
        !isDeepStrictEqual(
            vulnerabilities,
            expectedFalsePositiveVulnerabilities,
        )
    ) {
        reject('React Router 漏洞集合已变化，必须重新进行安全评估');
    }

    return {
        kind: 'exact-router-advisory-false-positive',
        message: `生产依赖审计通过：React Router ${expectedRouterVersion} 已包含官方 #15353 回移修复；${staleAdvisory} 的全局范围尚未同步，当前仅接受这一个精确误报`,
    };
};

export const evaluateAuditResult = ({ stdout, stderr = '', status }) =>
    validateAuditReport(parseAuditReport(stdout, stderr), status);

const readRegularFileWithoutFollowingLinks = (target) => {
    let descriptor;
    try {
        descriptor = openSync(target, constants.O_RDONLY | constants.O_NOFOLLOW);
        if (!fstatSync(descriptor).isFile()) {
            reject(`安全审计只允许读取普通文件（${target}）`);
        }
        return readFileSync(descriptor, 'utf8');
    } catch (error) {
        if (error instanceof SecurityAuditError) {
            throw error;
        }
        reject(`无法安全读取源文件 ${target}：${error.message}`);
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
            reject(`源码目录不允许符号链接，以免绕过安全审计（${target}）`);
        }
        if (entry.isDirectory()) {
            scanFiles(target);
            continue;
        }
        if (!entry.isFile() || !sourceExtensions.has(extname(target))) {
            continue;
        }

        const source = readRegularFileWithoutFollowingLinks(target);
        const marker = unstableRSCMarkers.find((candidate) =>
            source.includes(candidate),
        );
        if (marker) {
            reject(`ChronoDesk 前端不允许启用 unstable RSC 标记 ${marker}（${target}）`);
        }
        const unsafeCredentialPattern =
            unsafeTrustedDeviceCredentialPatterns.find((candidate) =>
                candidate.test(source),
            );
        if (unsafeCredentialPattern) {
            reject(`可信设备凭据不得进入前端脚本、JSON 或 Web Storage（${target}）`);
        }
    }
};

const validateInstalledRouter = () => {
    const routerDOMPackageURL = new URL(
        '../node_modules/react-router-dom/package.json',
        import.meta.url,
    );
    const routerDOMPackage = JSON.parse(
        readFileSync(routerDOMPackageURL, 'utf8'),
    );
    const routerDependencyName = Object.keys(
        routerDOMPackage.dependencies ?? {},
    ).find((name) => name === 'react-router');
    if (!routerDependencyName) {
        reject('react-router-dom 未声明 react-router 运行时依赖');
    }

    const requireFromRouterDOM = createRequire(routerDOMPackageURL);
    const routerPackagePath = requireFromRouterDOM.resolve(
        `${routerDependencyName}/package.json`,
    );
    const routerPackage = JSON.parse(readFileSync(routerPackagePath, 'utf8'));
    const routerChangelog = readFileSync(
        join(dirname(routerPackagePath), 'CHANGELOG.md'),
        'utf8',
    );
    validateRouterInstallation({
        routerVersion: routerPackage.version,
        routerChangelog,
    });
};

export const runSecurityAudit = () => {
    validateInstalledRouter();
    scanFiles(fileURLToPath(new URL('../src', import.meta.url)));

    const result = spawnSync('npm', ['audit', '--omit=dev', '--json'], {
        cwd: new URL('..', import.meta.url),
        encoding: 'utf8',
    });
    if (result.error) {
        reject(`无法执行 npm audit：${result.error.message}`);
    }

    return evaluateAuditResult({
        stdout: result.stdout,
        stderr: result.stderr,
        status: result.status,
    });
};

const isDirectExecution =
    typeof process.argv[1] === 'string' &&
    resolve(process.argv[1]) === fileURLToPath(import.meta.url);

if (isDirectExecution) {
    try {
        console.log(runSecurityAudit().message);
    } catch (error) {
        const message =
            error instanceof Error ? error.message : '依赖安全审计发生未知错误';
        console.error(`依赖安全审计失败：${message}`);
        process.exitCode = 1;
    }
}
