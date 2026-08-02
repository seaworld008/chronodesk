import assert from 'node:assert/strict';
import test from 'node:test';

import {
    evaluateAuditResult,
    expectedBackportEntry,
    expectedRouterVersion,
    SecurityAuditError,
    staleAdvisory,
    validateRouterInstallation,
} from './audit-security.mjs';

const metadata = (total) => ({
    vulnerabilities: {
        info: 0,
        low: 0,
        moderate: 0,
        high: total,
        critical: 0,
        total,
    },
});

const exactFalsePositiveReport = () => ({
    auditReportVersion: 2,
    vulnerabilities: {
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
            fixAvailable: {
                name: 'react-router-dom',
                version: '7.11.0',
                isSemVerMajor: true,
            },
        },
        'react-router-dom': {
            name: 'react-router-dom',
            severity: 'high',
            isDirect: true,
            via: ['react-router'],
            effects: [],
            range: '>=7.12.0-pre.0',
            nodes: ['node_modules/react-router-dom'],
            fixAvailable: {
                name: 'react-router-dom',
                version: '7.11.0',
                isSemVerMajor: true,
            },
        },
    },
    metadata: metadata(2),
});

const evaluate = (report, status) =>
    evaluateAuditResult({
        stdout: JSON.stringify(report),
        status,
    });

test('accepts the one exact React Router advisory false positive', () => {
    assert.deepEqual(evaluate(exactFalsePositiveReport(), 1), {
        kind: 'exact-router-advisory-false-positive',
        message: `生产依赖审计通过：React Router ${expectedRouterVersion} 已包含官方 #15353 回移修复；${staleAdvisory} 的全局范围尚未同步，当前仅接受这一个精确误报`,
    });
});

test('accepts a consistent zero-vulnerability npm audit result', () => {
    const result = evaluate(
        {
            auditReportVersion: 2,
            vulnerabilities: {},
            metadata: metadata(0),
        },
        0,
    );

    assert.equal(result.kind, 'clean');
});

test('rejects an additional advisory on the same Router package', () => {
    const report = exactFalsePositiveReport();
    report.vulnerabilities['react-router'].via.push({
        url: 'https://github.com/advisories/GHSA-unapproved',
    });

    assert.throws(
        () => evaluate(report, 1),
        (error) =>
            error instanceof SecurityAuditError &&
            error.message.includes('漏洞集合已变化'),
    );
});

test('rejects a malformed Router advisory chain with a controlled error', () => {
    const report = exactFalsePositiveReport();
    report.vulnerabilities['react-router'].via = {
        url: staleAdvisory,
    };

    assert.throws(
        () => evaluate(report, 1),
        (error) =>
            error instanceof SecurityAuditError &&
            error.message.includes('漏洞集合已变化'),
    );
});

test('rejects any critical npm audit field or structure drift', async (t) => {
    const driftCases = [
        [
            'advisory source',
            (report) => {
                report.vulnerabilities['react-router'].via[0].source = 9999999;
            },
        ],
        [
            'advisory name',
            (report) => {
                report.vulnerabilities['react-router'].via[0].name =
                    'unexpected';
            },
        ],
        [
            'advisory dependency',
            (report) => {
                report.vulnerabilities[
                    'react-router'
                ].via[0].dependency = 'unexpected';
            },
        ],
        [
            'advisory severity',
            (report) => {
                report.vulnerabilities['react-router'].via[0].severity =
                    'critical';
            },
        ],
        [
            'advisory range',
            (report) => {
                report.vulnerabilities['react-router'].via[0].range =
                    '>=7.0.0';
            },
        ],
        [
            'router package name',
            (report) => {
                report.vulnerabilities['react-router'].name = 'unexpected';
            },
        ],
        [
            'router directness',
            (report) => {
                report.vulnerabilities['react-router'].isDirect = true;
            },
        ],
        [
            'router range',
            (report) => {
                report.vulnerabilities['react-router'].range = '>=7.0.0';
            },
        ],
        [
            'router node path',
            (report) => {
                report.vulnerabilities['react-router'].nodes = [
                    'node_modules/unexpected',
                ];
            },
        ],
        [
            'router effects',
            (report) => {
                report.vulnerabilities['react-router'].effects = [];
            },
        ],
        [
            'router fix target',
            (report) => {
                report.vulnerabilities[
                    'react-router'
                ].fixAvailable.version = '8.3.0';
            },
        ],
        [
            'router-dom package name',
            (report) => {
                report.vulnerabilities['react-router-dom'].name = 'unexpected';
            },
        ],
        [
            'router-dom directness',
            (report) => {
                report.vulnerabilities['react-router-dom'].isDirect = false;
            },
        ],
        [
            'router-dom range',
            (report) => {
                report.vulnerabilities['react-router-dom'].range = '>=7.0.0';
            },
        ],
        [
            'router-dom node path',
            (report) => {
                report.vulnerabilities['react-router-dom'].nodes = [
                    'node_modules/unexpected',
                ];
            },
        ],
        [
            'vulnerability metadata count',
            (report) => {
                report.metadata.vulnerabilities.high = 1;
            },
        ],
        [
            'unexpected top-level vulnerability field',
            (report) => {
                report.vulnerabilities['react-router'].unexpected = true;
            },
        ],
    ];

    for (const [name, mutate] of driftCases) {
        await t.test(name, () => {
            const report = exactFalsePositiveReport();
            mutate(report);
            assert.throws(
                () => evaluate(report, 1),
                (error) =>
                    error instanceof SecurityAuditError &&
                    error.message.includes('漏洞集合已变化'),
            );
        });
    }
});

test('rejects an unexpected installed Router version', () => {
    assert.throws(
        () =>
            validateRouterInstallation({
                routerVersion: '7.18.1',
                routerChangelog: expectedBackportEntry,
            }),
        (error) =>
            error instanceof SecurityAuditError &&
            error.message.includes('实际版本为 7.18.1'),
    );
});

test('rejects a Router package without the v7 backport changelog entry', () => {
    assert.throws(
        () =>
            validateRouterInstallation({
                routerVersion: expectedRouterVersion,
                routerChangelog: '## v7.18.2\n\n### Patch Changes',
            }),
        (error) =>
            error instanceof SecurityAuditError &&
            error.message.includes('缺少官方 #15353'),
    );
});

test('rejects invalid npm audit JSON without consulting the network', () => {
    assert.throws(
        () =>
            evaluateAuditResult({
                stdout: '{not-json',
                stderr: 'fixture parse failure',
                status: 1,
            }),
        (error) =>
            error instanceof SecurityAuditError &&
            error.message.includes('未返回有效 JSON') &&
            error.message.includes('fixture parse failure'),
    );
});

test('rejects incomplete npm audit JSON structures', () => {
    const incompleteReports = [
        {},
        {
            auditReportVersion: 2,
            vulnerabilities: {},
        },
        {
            auditReportVersion: 2,
            vulnerabilities: {},
            metadata: { vulnerabilities: {} },
        },
    ];

    for (const report of incompleteReports) {
        assert.throws(
            () => evaluate(report, 0),
            (error) =>
                error instanceof SecurityAuditError &&
                error.message.includes('结构不完整'),
        );
    }
});

test('rejects a zero-vulnerability body with a failing audit status', () => {
    assert.throws(
        () =>
            evaluate(
                {
                    auditReportVersion: 2,
                    vulnerabilities: {},
                    metadata: metadata(0),
                },
                1,
            ),
        (error) =>
            error instanceof SecurityAuditError &&
            error.message.includes('零漏洞结果与退出状态不一致'),
    );
});
