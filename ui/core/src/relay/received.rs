//! Exact received-ID membership with compact contiguous ranges. Gaps stay gaps.
use serde::{Deserialize, Deserializer, Serialize, Serializer, ser::SerializeSeq};
use std::collections::BTreeMap;

#[derive(Clone, Default)]
pub struct ReceivedIds(BTreeMap<u64, u64>);
impl ReceivedIds {
    pub fn contains(&self, text: &str) -> bool {
        let Some(id) = decimal(text) else {
            return false;
        };
        self.0
            .range(..=id)
            .next_back()
            .is_some_and(|(_, end)| *end >= id)
    }
    pub fn insert(&mut self, text: String) -> bool {
        let Some(id) = decimal(&text) else {
            return false;
        };
        let left = self.0.range(..=id).next_back().map(|(&a, &b)| (a, b));
        if left.is_some_and(|(_, end)| end >= id) {
            return false;
        }
        let right = self.0.range(id..).next().map(|(&a, &b)| (a, b));
        let mut start = id;
        let mut end = id;
        if let Some((a, b)) = left.filter(|(_, b)| b.checked_add(1) == Some(id)) {
            let _ = b;
            start = a;
            self.0.remove(&a);
        }
        if let Some((a, b)) = right.filter(|(a, _)| id.checked_add(1) == Some(*a)) {
            end = b;
            self.0.remove(&a);
        }
        self.0.insert(start, end);
        true
    }
    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
    pub fn len(&self) -> usize {
        let count: u64 = self.0.iter().map(|(a, b)| b - a + 1).sum();
        usize::try_from(count).unwrap_or(usize::MAX)
    }
}
fn decimal(text: &str) -> Option<u64> {
    let value = text.parse::<u64>().ok()?;
    (value > 0 && value.to_string() == text).then_some(value)
}
impl Serialize for ReceivedIds {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        let mut sequence = serializer.serialize_seq(Some(self.0.len()))?;
        for (first, last) in &self.0 {
            // Strings preserve the entire uint64 range through Android JSONObject.
            sequence.serialize_element(&(first.to_string(), last.to_string()))?;
        }
        sequence.end()
    }
}
impl<'de> Deserialize<'de> for ReceivedIds {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let ranges = Vec::<(String, String)>::deserialize(deserializer)?;
        let mut out = Self::default();
        let mut previous = None::<u64>;
        for (first, last) in ranges {
            let a = decimal(&first)
                .ok_or_else(|| serde::de::Error::custom("Invalid received ID range."))?;
            let b = decimal(&last)
                .ok_or_else(|| serde::de::Error::custom("Invalid received ID range."))?;
            if a > b || previous.is_some_and(|p| p.checked_add(1).is_none_or(|next| a <= next)) {
                return Err(serde::de::Error::custom("Invalid received ID ranges."));
            }
            out.0.insert(a, b);
            previous = Some(b);
        }
        Ok(out)
    }
}
