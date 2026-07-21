// Locating a model-returned excerpt inside extracted document text is fuzzier
// than it looks. The model re-types the passage, so apostrophes, quotes and
// dashes come back as different Unicode code points than the PDF extractor
// produced — Uzbek (oʻ/o'/o‘), French quotes, en/em dashes and non-breaking
// spaces all differ by glyph while reading identically. Exact substring matching
// silently finds nothing.
//
// normalize() folds those variants together; findExcerpt() then degrades
// gracefully: whole excerpt → leading clause → first few words.

const PUNCT_FOLD = [
  [/[‘’ʻʼʹ′`´]/g, "'"], // apostrophe variants
  [/[“”«»″]/g, '"'],                   // quote variants
  [/[‐-―−]/g, '-'],                              // dash variants
  [/[   ​]/g, ' '],                         // exotic spaces
]

export function normalize(s) {
  let out = (s || '')
  for (const [re, to] of PUNCT_FOLD) out = out.replace(re, to)
  return out.replace(/\s+/g, ' ').trim().toLowerCase()
}

// Progressively shorter needles: the full excerpt, then its first clause, then
// its first words. A shorter needle is likelier to survive re-typing.
export function needleVariants(excerpt) {
  const full = normalize(excerpt)
  if (!full) return []
  const out = [full]
  const clause = full.split(/[.;:]/)[0].trim()
  if (clause.length >= 20 && clause !== full) out.push(clause)
  const words = full.split(' ')
  for (const n of [10, 6]) {
    if (words.length > n) {
      const w = words.slice(0, n).join(' ')
      if (w.length >= 12) out.push(w)
    }
  }
  return out
}

// findExcerpt returns { index, needle } for the first variant present in
// normalized haystack, or null.
export function findExcerpt(haystack, excerpt) {
  const hay = normalize(haystack)
  if (!hay) return null
  for (const needle of needleVariants(excerpt)) {
    const index = hay.indexOf(needle)
    if (index >= 0) return { index, needle }
  }
  return null
}

// pageContaining returns the index of the first page containing the excerpt.
export function pageContaining(pages, excerpt) {
  if (!pages?.length) return -1
  for (const needle of needleVariants(excerpt)) {
    const i = pages.findIndex((p) => normalize(p).includes(needle))
    if (i >= 0) return i
  }
  return -1
}

// locateSpan maps a match back to raw-text offsets so the caller can wrap it in
// a <mark>. Returns [start, end] or null.
export function locateSpan(text, excerpt) {
  const hit = findExcerpt(text, excerpt)
  if (!hit) return null
  const norm = normalize(text)
  const { index, needle } = hit

  // Walk raw text and normalized text together to translate the offset.
  let ri = 0, ni = 0, start = -1, end = -1
  while (ri < text.length && ni <= norm.length) {
    if (ni === index && start < 0) start = ri
    if (ni === index + needle.length) { end = ri; break }
    if (/\s/.test(text[ri])) {
      let j = ri
      while (j < text.length && /\s/.test(text[j])) j++
      if (ni > 0 && norm[ni] === ' ') ni++
      ri = j
    } else { ri++; ni++ }
  }
  if (start < 0) return null
  if (end < 0) end = text.length
  return [start, end]
}
