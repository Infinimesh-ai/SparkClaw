"""Stable identity of a slide shape shared by visual QA and visual repair.

QA records the hash of every top-level shape it offers as a repair target;
repair recomputes it on the candidate it is about to mutate and refuses any
shape whose bytes drifted in between. Both sides must therefore hash the
same canonical form of the same inputs, which is why this lives in one place.
"""
import hashlib

from lxml import etree


def shape_target_hash(shape_ref, shape):
    """Return the SHA-256 of the shape_ref plus the shape's exclusive-C14N XML."""
    canonical = etree.tostring(shape._element, method="c14n", exclusive=True, with_comments=False)
    payload = shape_ref.encode("utf-8") + b"\0" + canonical
    return hashlib.sha256(payload).hexdigest()
