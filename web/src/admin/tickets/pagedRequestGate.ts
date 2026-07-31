export class LatestRequestGate {
    private sequence = 0
    private controller?: AbortController

    start() {
        this.controller?.abort()
        this.controller = new AbortController()
        this.sequence += 1
        return {
            token: this.sequence,
            signal: this.controller.signal,
        }
    }

    isCurrent(token: number) {
        return token === this.sequence && !this.controller?.signal.aborted
    }

    abort() {
        this.controller?.abort()
        this.sequence += 1
    }
}

export const lastPageAfterAppend = (currentTotal: number, pageSize = 25) =>
    Math.max(1, Math.ceil((currentTotal + 1) / pageSize))
