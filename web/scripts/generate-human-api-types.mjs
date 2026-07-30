import { readFile, writeFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const generatorVersion = '2.0.0'
const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const webDirectory = path.resolve(scriptDirectory, '..')
const repositoryDirectory = path.resolve(webDirectory, '..')
const sourcePath = path.join(
    repositoryDirectory,
    'server',
    'internal',
    'humanopenapi',
    'openapi.json',
)
const outputPath = path.join(
    webDirectory,
    'src',
    'lib',
    'generated',
    'human-api.ts',
)

const sourceText = await readFile(sourcePath, 'utf8')
const source = JSON.parse(sourceText)
const contractDigest = createHash('sha256').update(sourceText).digest('hex')
if (source.openapi !== '3.2.0') {
    throw new Error(`Human Web contract must use OpenAPI 3.2.0, got ${source.openapi}`)
}
if (source['x-chronodesk-types-generator'] !== generatorVersion) {
    throw new Error(
        `Human Web generator version mismatch: contract=${source['x-chronodesk-types-generator']} runtime=${generatorVersion}`,
    )
}

const schemas = source.components?.schemas
if (!schemas || typeof schemas !== 'object' || Array.isArray(schemas)) {
    throw new Error('Human Web contract has no components.schemas object')
}

const operations = collectOperations(source)
const generated = [
    '/**',
    ' * Generated from server/internal/humanopenapi/openapi.json.',
    ` * Generator: chronodesk-human-openapi-types@${generatorVersion}.`,
    ` * Contract SHA-256: ${contractDigest}.`,
    ' * Do not edit by hand; run `npm run generate:human-api`.',
    ' */',
    '',
    ...Object.entries(schemas).flatMap(([name, schema]) =>
        generatedSchema(name, schema),
    ),
    ...operations.flatMap((operation) => generatedOperationTypes(operation)),
    ...generatedOperationRuntime(operations),
].join('\n')

if (process.argv.includes('--check')) {
    let current
    try {
        current = await readFile(outputPath, 'utf8')
    } catch (error) {
        if (error && error.code === 'ENOENT') {
            throw new Error(
                'Generated Human Web types are missing; run `npm run generate:human-api`.',
            )
        }
        throw error
    }
    if (current !== generated) {
        throw new Error(
            'Generated Human Web types are stale; run `npm run generate:human-api` and commit the result.',
        )
    }
} else {
    await writeFile(outputPath, generated)
}

function generatedSchema(name, schema) {
    const runtimeValuesName = schema['x-chronodesk-runtime-values']
    if (runtimeValuesName === undefined) {
        return [`export type ${name} = ${schemaType(schema, 0)}`, '']
    }
    if (
        typeof runtimeValuesName !== 'string' ||
        !/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(runtimeValuesName)
    ) {
        throw new Error(
            `${name}.x-chronodesk-runtime-values must be a JavaScript identifier`,
        )
    }
    if (!Array.isArray(schema.enum) || schema.enum.length === 0) {
        throw new Error(
            `${name}.x-chronodesk-runtime-values requires a non-empty enum`,
        )
    }
    return [
        `export const ${runtimeValuesName} = ${JSON.stringify(schema.enum)} as const`,
        `export type ${name} = (typeof ${runtimeValuesName})[number]`,
        '',
    ]
}

function schemaType(schema, depth) {
    if (schema === true) return 'unknown'
    if (schema === false) return 'never'
    if (!schema || typeof schema !== 'object' || Array.isArray(schema)) {
        throw new Error(`Unsupported schema node: ${JSON.stringify(schema)}`)
    }
    if ('$ref' in schema) {
        return referenceName(schema.$ref)
    }
    if (Array.isArray(schema.enum)) {
        return schema.enum.map((value) => JSON.stringify(value)).join(' | ')
    }
    if ('const' in schema) {
        return JSON.stringify(schema.const)
    }
    if (Array.isArray(schema.allOf)) {
        return schema.allOf.map((item) => schemaType(item, depth)).join(' & ')
    }
    if (Array.isArray(schema.oneOf)) {
        return schema.oneOf.map((item) => schemaType(item, depth)).join(' | ')
    }
    if (Array.isArray(schema.anyOf)) {
        return schema.anyOf.map((item) => schemaType(item, depth)).join(' | ')
    }
    if (Array.isArray(schema.type)) {
        return schema.type
            .map((type) => schemaType({ ...schema, type }, depth))
            .join(' | ')
    }

    switch (schema.type) {
        case 'null':
            return 'null'
        case 'string':
            return 'string'
        case 'integer':
        case 'number':
            return 'number'
        case 'boolean':
            return 'boolean'
        case 'array':
            return `Array<${schemaType(schema.items ?? {}, depth)}>`
        case 'object':
        case undefined:
            return objectType(schema, depth)
        default:
            throw new Error(`Unsupported OpenAPI schema type ${JSON.stringify(schema.type)}`)
    }
}

function objectType(schema, depth) {
    const properties = schema.properties ?? {}
    const entries = Object.entries(properties)
    if (entries.length === 0) {
        if (schema.additionalProperties && typeof schema.additionalProperties === 'object') {
            return `{ [key: string]: ${schemaType(schema.additionalProperties, depth)} }`
        }
        return schema.additionalProperties === false ? 'Record<string, never>' : 'unknown'
    }

    const required = new Set(schema.required ?? [])
    const indentation = ' '.repeat((depth + 1) * 4)
    const closingIndentation = ' '.repeat(depth * 4)
    const fields = entries.map(([name, property]) => {
        const optional = required.has(name) ? '' : '?'
        return `${indentation}${propertyName(name)}${optional}: ${schemaType(property, depth + 1)}`
    })
    return `{\n${fields.join('\n')}\n${closingIndentation}}`
}

function referenceName(reference) {
    const prefix = '#/components/schemas/'
    if (typeof reference !== 'string' || !reference.startsWith(prefix)) {
        throw new Error(`Only local component schema references are supported: ${reference}`)
    }
    return reference.slice(prefix.length)
}

function propertyName(name) {
    return /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name) ? name : JSON.stringify(name)
}

function collectOperations(document) {
    const paths = document.paths
    if (!paths || typeof paths !== 'object' || Array.isArray(paths)) {
        throw new Error('Human Web contract has no paths object')
    }

    const result = []
    const seen = new Set()
    for (const [operationPath, pathItem] of Object.entries(paths)) {
        if (!pathItem || typeof pathItem !== 'object' || Array.isArray(pathItem)) {
            throw new Error(`Invalid path item ${operationPath}`)
        }
        for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
            const operation = pathItem[method]
            if (operation === undefined) continue
            if (!operation || typeof operation !== 'object' || Array.isArray(operation)) {
                throw new Error(`Invalid ${method.toUpperCase()} ${operationPath}`)
            }
            const operationId = operation.operationId
            if (
                typeof operationId !== 'string'
                || !/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(operationId)
            ) {
                throw new Error(
                    `${method.toUpperCase()} ${operationPath} needs a JavaScript-safe operationId`,
                )
            }
            if (seen.has(operationId)) {
                throw new Error(`Duplicate Human Web operationId ${operationId}`)
            }
            seen.add(operationId)

            const parameters = [
                ...(Array.isArray(pathItem.parameters) ? pathItem.parameters : []),
                ...(Array.isArray(operation.parameters) ? operation.parameters : []),
            ].map((parameter) => resolveComponent(parameter, 'parameters'))
            const pathParameters = parameters.filter(
                (parameter) => parameter.in === 'path',
            )
            const queryParameters = parameters.filter(
                (parameter) => parameter.in === 'query',
            )
            validatePathParameters(operationPath, pathParameters)

            const response = successfulResponse(operation.responses)
            const requestBody = requestBodyDefinition(operation.requestBody)
            result.push({
                operationId,
                typeName: `${pascalCase(operationId)}Operation`,
                method: method.toUpperCase(),
                path: operationPath,
                pathParameters,
                queryParameters,
                requestSchema: requestBody?.schema,
                requestBodyMode: requestBody === undefined
                    ? 'none'
                    : requestBody.required
                        ? 'required'
                        : 'optional',
                responseSchema: response.schema,
                successStatus: response.status,
            })
        }
    }
    if (result.length === 0) {
        throw new Error('Human Web contract has no operations')
    }
    return result
}

function resolveComponent(value, componentType) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) {
        throw new Error(`Invalid OpenAPI ${componentType} value`)
    }
    if (!('$ref' in value)) return value
    const prefix = `#/components/${componentType}/`
    if (typeof value.$ref !== 'string' || !value.$ref.startsWith(prefix)) {
        throw new Error(`Unsupported ${componentType} reference ${value.$ref}`)
    }
    const name = value.$ref.slice(prefix.length)
    const resolved = source.components?.[componentType]?.[name]
    if (!resolved) {
        throw new Error(`Missing ${componentType} component ${name}`)
    }
    return resolved
}

function validatePathParameters(operationPath, parameters) {
    const placeholders = [
        ...operationPath.matchAll(/\{([A-Za-z_$][A-Za-z0-9_$]*)\}/g),
    ].map((match) => match[1])
    const names = parameters.map((parameter) => parameter.name)
    if (
        placeholders.length !== names.length
        || placeholders.some((name) => !names.includes(name))
    ) {
        throw new Error(
            `${operationPath} path parameters ${JSON.stringify(names)} do not match placeholders ${JSON.stringify(placeholders)}`,
        )
    }
}

function requestBodyDefinition(rawRequestBody) {
    if (rawRequestBody === undefined) return undefined
    const requestBody = resolveComponent(rawRequestBody, 'requestBodies')
    const schema = preferredMediaSchema(requestBody.content)
    if (!schema) {
        throw new Error('Human Web request body has no typed media schema')
    }
    return {
        schema,
        required: requestBody.required === true,
    }
}

function successfulResponse(rawResponses) {
    if (!rawResponses || typeof rawResponses !== 'object') {
        throw new Error('Human Web operation has no responses')
    }
    const status = Object.keys(rawResponses)
        .filter((candidate) => /^2\d\d$/.test(candidate))
        .sort()[0]
    if (!status) {
        throw new Error('Human Web operation has no 2xx response')
    }
    const response = resolveComponent(rawResponses[status], 'responses')
    return {
        status: Number(status),
        schema: preferredMediaSchema(response.content),
    }
}

function preferredMediaSchema(content) {
    if (!content || typeof content !== 'object' || Array.isArray(content)) {
        return undefined
    }
    const mediaTypes = Object.keys(content).sort()
    const mediaType = mediaTypes.includes('application/json')
        ? 'application/json'
        : mediaTypes[0]
    return mediaType === undefined ? undefined : content[mediaType]?.schema
}

function generatedOperationTypes(operation) {
    const pathType = parameterObjectType(operation.pathParameters)
    const queryType = parameterObjectType(operation.queryParameters)
    const requestType = operation.requestSchema
        ? schemaType(operation.requestSchema, 0)
        : 'never'
    const responseType = operation.responseSchema
        ? schemaType(operation.responseSchema, 0)
        : 'void'
    return [
        `export type ${operation.typeName}PathParameters = ${pathType}`,
        `export type ${operation.typeName}Query = ${queryType}`,
        `export type ${operation.typeName}Request = ${requestType}`,
        `export type ${operation.typeName}Response = ${responseType}`,
        '',
    ]
}

function parameterObjectType(parameters) {
    if (parameters.length === 0) return 'Record<string, never>'
    const fields = parameters.map((parameter) => {
        if (typeof parameter.name !== 'string' || parameter.name === '') {
            throw new Error('Human Web parameter needs a name')
        }
        const optional = parameter.required === true ? '' : '?'
        const schema = parameter.schema
        if (!schema) {
            throw new Error(`Human Web parameter ${parameter.name} needs a schema`)
        }
        return `    ${propertyName(parameter.name)}${optional}: ${schemaType(schema, 1)}`
    })
    return `{\n${fields.join('\n')}\n}`
}

function generatedOperationRuntime(operations) {
    const mapEntries = operations.map((operation) =>
        [
            `    ${propertyName(operation.operationId)}: {`,
            `        method: ${JSON.stringify(operation.method)},`,
            `        path: ${JSON.stringify(operation.path)},`,
            `        successStatus: ${operation.successStatus},`,
            `        requestBody: ${JSON.stringify(operation.requestBodyMode)},`,
            '    },',
        ].join('\n'),
    )
    const typeEntries = operations.map((operation) =>
        [
            `    ${propertyName(operation.operationId)}: {`,
            `        pathParameters: ${operation.typeName}PathParameters`,
            `        query: ${operation.typeName}Query`,
            `        request: ${operation.typeName}Request`,
            `        response: ${operation.typeName}Response`,
            '    }',
        ].join('\n'),
    )
    const routeEntries = operations.map((operation) => {
        const hasPathParameters = operation.pathParameters.length > 0
        const argumentsList = hasPathParameters
            ? `pathParameters: ${operation.typeName}PathParameters, query: ${operation.typeName}Query = {}`
            : `query: ${operation.typeName}Query = {}`
        const pathArguments = hasPathParameters ? 'pathParameters' : '{}'
        return [
            `    ${propertyName(operation.operationId)}: (${argumentsList}) =>`,
            `        humanApiRoute(${JSON.stringify(operation.operationId)}, ${pathArguments}, query),`,
        ].join('\n')
    })
    return [
        'export const humanApiOperations = {',
        ...mapEntries,
        '} as const',
        '',
        'export interface HumanApiOperationTypes {',
        ...typeEntries,
        '}',
        '',
        'export type HumanApiOperationId = keyof HumanApiOperationTypes',
        '',
        'export type HumanApiPathParameters<Operation extends HumanApiOperationId> =',
        '    HumanApiOperationTypes[Operation]["pathParameters"]',
        'export type HumanApiQuery<Operation extends HumanApiOperationId> =',
        '    HumanApiOperationTypes[Operation]["query"]',
        'export type HumanApiRequest<Operation extends HumanApiOperationId> =',
        '    HumanApiOperationTypes[Operation]["request"]',
        'export type HumanApiResponse<Operation extends HumanApiOperationId> =',
        '    HumanApiOperationTypes[Operation]["response"]',
        '',
        'type HumanApiRequestBodyOption<Operation extends HumanApiOperationId> =',
        '    (typeof humanApiOperations)[Operation]["requestBody"] extends "required"',
        '        ? { body: HumanApiRequest<Operation> }',
        '        : (typeof humanApiOperations)[Operation]["requestBody"] extends "optional"',
        '            ? { body?: HumanApiRequest<Operation> }',
        '            : { body?: never }',
        '',
        'export type HumanApiRequestOptions<Operation extends HumanApiOperationId> = {',
        '    pathParameters: HumanApiPathParameters<Operation>',
        '    query?: HumanApiQuery<Operation>',
        '} & HumanApiRequestBodyOption<Operation>',
        '',
        'export type HumanApiClientRequest<Operation extends HumanApiOperationId> = {',
        '    operationId: Operation',
        '    method: (typeof humanApiOperations)[Operation]["method"]',
        '    path: string',
        '    body?: HumanApiRequest<Operation>',
        '}',
        '',
        'export const humanApiRoute = <Operation extends HumanApiOperationId>(',
        '    operationId: Operation,',
        '    pathParameters: HumanApiPathParameters<Operation>,',
        '    query: HumanApiQuery<Operation> = {} as HumanApiQuery<Operation>,',
        '): string => {',
        '    const operation = humanApiOperations[operationId]',
        '    const parameters = pathParameters as Record<string, string | number>',
        '    const route = operation.path.replace(/\\{([^}]+)\\}/g, (_, name: string) => {',
        '        const value = parameters[name]',
        '        if (value === undefined || value === null || String(value) === "") {',
        '            throw new Error(`Missing Human API path parameter ${name}`)',
        '        }',
        '        return encodeURIComponent(String(value))',
        '    })',
        '    const search = new URLSearchParams()',
        '    for (const [name, rawValue] of Object.entries(query)) {',
        '        if (rawValue === undefined || rawValue === null || rawValue === "") continue',
        '        const values = Array.isArray(rawValue) ? rawValue : [rawValue]',
        '        for (const value of values) search.append(name, String(value))',
        '    }',
        '    const encoded = search.toString()',
        '    return encoded === "" ? route : `${route}?${encoded}`',
        '}',
        '',
        'export const buildHumanApiRequest = <Operation extends HumanApiOperationId>(',
        '    operationId: Operation,',
        '    options: HumanApiRequestOptions<Operation>,',
        '): HumanApiClientRequest<Operation> => {',
        '    const operation = humanApiOperations[operationId]',
        '    const candidate = options as HumanApiRequestOptions<Operation> & {',
        '        body?: HumanApiRequest<Operation>',
        '    }',
        '    return {',
        '        operationId,',
        '        method: operation.method,',
        '        path: humanApiRoute(',
        '            operationId,',
        '            options.pathParameters,',
        '            options.query,',
        '        ),',
        '        ...("body" in candidate ? { body: candidate.body } : {}),',
        '    }',
        '}',
        '',
        'export const humanApiRoutes = {',
        ...routeEntries,
        '} as const',
        '',
    ]
}

function pascalCase(value) {
    return value
        .replace(/[^A-Za-z0-9_$]+(.)/g, (_, character) => character.toUpperCase())
        .replace(/^[a-z]/, (character) => character.toUpperCase())
}
