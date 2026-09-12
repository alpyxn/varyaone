package backup

// Windows has no O_NOFOLLOW. The snapshot walk already rejects every
// non-regular entry, and copyFile re-checks the file type through the open
// descriptor, so the race this flag closes on Unix is caught one step later
// here instead of being prevented.
const openNoFollowFlag = 0
