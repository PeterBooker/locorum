import type { sites } from '../../../wailsjs/go/models';
import { PlayIcon } from '@heroicons/react/24/solid';

import { useParams } from 'react-router';

export default function Site({ sites }: { sites: sites.Site[] }) {
    let { siteId } = useParams();

    const site = sites.find((site) => site.id === siteId);

    if (!site) {
        return null;
    }

    return (
        <div className="site">
            <PlayIcon className="h-5 w-5 text-gray-500" />
            <h1>{ site.name }</h1>

            <p>{ site.url }</p>
        </div>
    )
}
