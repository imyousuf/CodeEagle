import lodash from 'lodash';
import { validate } from './validators';

function helper() {
    return 42;
}

function processItems(items) {
    const result = helper();
    const valid = validate(items);
    const sorted = lodash.sortBy(items, 'name');
    return sorted;
}

class ItemProcessor {
    transform(item) {
        return item;
    }

    process(item) {
        const transformed = this.transform(item);
        return lodash.cloneDeep(transformed);
    }
}
