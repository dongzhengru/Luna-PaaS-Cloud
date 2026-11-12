// Preserve UTF-8 characters and CRLF sequences split across stream chunks.
// A lone carriage return is displayed as a new line in this plain-text viewer.
export class LogDecoder {
  private decoder = new TextDecoder()
  private previousCR = false

  decode(bytes: Uint8Array): string {
    let text = this.decoder.decode(bytes, { stream: true })
    if (!text) return ''
    const endsWithCR = text.endsWith('\r')
    if (this.previousCR && text.startsWith('\n')) text = text.slice(1)
    this.previousCR = endsWithCR
    return text.replace(/\r\n?/g, '\n')
  }
}
